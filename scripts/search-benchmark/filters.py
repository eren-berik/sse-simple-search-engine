"""Times the tag and publication date filters (count + page, as the service runs them) on
bench.contents (see setup.sql).

Run from the repository root with the compose stack up, after setup.sql:
    python3 scripts/search-benchmark/filters.py

To see the date filter without its index, drop it first and rebuild it afterwards:
    docker compose exec -T postgres psql -U app -d contents -c 'DROP INDEX bench.contents_published_at_idx'
    docker compose exec -T postgres psql -U app -d contents -c 'CREATE INDEX ON bench.contents (published_at)'
"""
import json
import subprocess
import sys

LISTED = "removed_at IS NULL AND EXISTS (SELECT 1 FROM bench.providers p WHERE p.name = c.provider AND p.enabled)"
COLS = "c.id, c.provider, c.external_id, c.title, c.type, c.views, c.likes, c.duration_sec, c.reading_time, c.reactions, c.comments, c.tags, c.published_at, c.score"
WEEK = "c.published_at >= now() - interval '7 days'"


def any_of(*tags):
    # tag_match=any
    return "c.tags && ARRAY[" + ",".join(f"'{t}'" for t in tags) + "]::text[]"


def all_of(*tags):
    # tag_match=all
    return "c.tags @> ARRAY[" + ",".join(f"'{t}'" for t in tags) + "]::text[]"


def scenario(name, *conds):
    where = "WHERE " + " AND ".join((LISTED,) + conds)
    count = f"SELECT count(*) FROM bench.contents c {where}"
    page = f"SELECT {COLS}, 0::float8 AS relevance FROM bench.contents c {where} ORDER BY score DESC, id ASC LIMIT 20"
    return name, count, page


def run(sql):
    out = subprocess.run(
        ["docker", "compose", "exec", "-T", "postgres", "psql", "-U", "app", "-d", "contents", "-tA", "-c",
         "SET max_parallel_workers_per_gather = 0; EXPLAIN (ANALYZE, FORMAT JSON) " + sql],
        capture_output=True, text=True, check=True).stdout
    plan = json.loads(out[out.index("["):])[0]
    return plan["Execution Time"], plan["Plan"]


def node_types(plan, acc=None):
    acc = acc if acc is not None else []
    acc.append(plan["Node Type"] + (f" ({plan['Index Name']})" if "Index Name" in plan else ""))
    for p in plan.get("Plans", []):
        node_types(p, acc)
    return acc


def matching_rows(plan):
    # The count's aggregate sits on the node producing the matching rows.
    return plan["Plans"][0]["Actual Rows"]


# Tag frequencies in bench.contents: "go" 3.6% of rows, "docker" 1.4%, "w4000" 173 rows.
scenarios = [
    scenario("no filter"),
    scenario("rare tag (w4000)", any_of("w4000")),
    scenario("common tag (go)", any_of("go")),
    scenario("any of go, docker, kubernetes", any_of("go", "docker", "kubernetes")),
    scenario("any of 10 common tags", any_of("go", "docker", "kubernetes", "programming", "tutorial",
                                              "advanced", "introduction", "guide", "patterns", "testing")),
    scenario("all of go, docker", all_of("go", "docker")),
    scenario("go + type=text", any_of("go"), "c.type = 'text'"),
    scenario("last 7 days", WEEK),
    scenario("go + last 7 days", any_of("go"), WEEK),
]

print(f"{'scenario':32} {'rows':>8} {'count':>9} {'page':>9}   page plan")
for name, count_sql, page_sql in scenarios:
    counts = [run(count_sql) for _ in range(3)]
    pages = [run(page_sql) for _ in range(3)]
    plan = " > ".join(dict.fromkeys(n for n in node_types(pages[0][1]) if n not in ("Limit", "Result")))
    print(f"{name:32} {matching_rows(counts[0][1]):>8} {min(t for t, _ in counts):7.1f}ms "
          f"{min(t for t, _ in pages):7.1f}ms   {plan[:100]}")
    sys.stdout.flush()
