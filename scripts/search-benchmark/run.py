"""Times the search service's SQL (count + page) on bench.contents (see setup.sql).

Run from the repository root with the compose stack up:
    docker compose exec -T postgres psql -U app -d contents -f - < scripts/search-benchmark/setup.sql
    python3 scripts/search-benchmark/run.py
"""
import json
import subprocess
import sys

LISTED = "removed_at IS NULL AND EXISTS (SELECT 1 FROM bench.providers p WHERE p.name = c.provider AND p.enabled)"
COLS = "c.id, c.provider, c.external_id, c.title, c.type, c.views, c.likes, c.duration_sec, c.reading_time, c.reactions, c.comments, c.tags, c.published_at, c.score"


def keyword(q, like=None):
    like = like or f"%{q}%"
    cond = f"(c.search_vector @@ websearch_to_tsquery('simple', '{q}') OR c.title ILIKE '{like}')"
    # Same as relevanceSQL in sse-service/internal/repository/postgres/content_repository.go.
    rel = (f"(CASE WHEN c.search_vector @@ websearch_to_tsquery('simple', '{q}') "
           f"THEN ts_rank('{{0.1, 0.2, 0.8, 1.0}}', c.search_vector, websearch_to_tsquery('simple', '{q}')) "
           f"ELSE 0.4 * word_similarity('{q}', c.title) END)::float8")
    return cond, rel


def scenario(name, extra=None, rel="0::float8", order="score DESC, id ASC", offset=0):
    conds = [LISTED] + ([extra] if extra else [])
    where = "WHERE " + " AND ".join(conds)
    count = f"SELECT count(*) FROM bench.contents c {where}"
    # Like the service: past page 1, relevance sorting also takes the best relevance of the whole
    # search (max ... OVER ()) for the match percentage; on page 1 it is the first row.
    best = "max(relevance) OVER ()" if order.startswith("relevance") and offset > 0 else "0::float8"
    page = (f"SELECT *, {best} AS best FROM (SELECT {COLS}, {rel} AS relevance FROM bench.contents c {where}) hits "
            f"ORDER BY {order} LIMIT 20 OFFSET {offset}")
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


go, go_rel = keyword("go")
docker, docker_rel = keyword("docker")
rare, rare_rel = keyword("w3000")
part, part_rel = keyword("kuberne")
scenarios = [
    scenario("no keyword, popularity, page 1"),
    scenario("no keyword, popularity, page 5000 (offset 100k)", offset=100000),
    scenario("type=text, popularity", extra="c.type = 'text'"),
    scenario("q=go (28% of rows), relevance", go, go_rel, "relevance DESC, score DESC, id ASC"),
    scenario("q=go, relevance, page 2", go, go_rel, "relevance DESC, score DESC, id ASC", offset=20),
    scenario("q=docker (9%), relevance", docker, docker_rel, "relevance DESC, score DESC, id ASC"),
    scenario("q=w3000 (rare, 769 rows), relevance", rare, rare_rel, "relevance DESC, score DESC, id ASC"),
    scenario("q=go, popularity", go, order="score DESC, id ASC"),
    scenario("q=kuberne (substring only), relevance", part, part_rel, "relevance DESC, score DESC, id ASC"),
    scenario("tag=docker, popularity", "c.tags @> ARRAY['docker']"),
]

print(f"{'scenario':48} {'count':>10} {'page':>10}   page plan")
for name, count_sql, page_sql in scenarios:
    best_count = min(run(count_sql)[0] for _ in range(3))
    times = [run(page_sql) for _ in range(3)]
    best_page = min(t for t, _ in times)
    plan = " > ".join(dict.fromkeys(n for n in node_types(times[0][1]) if n not in ("Limit", "Result")))
    print(f"{name:48} {best_count:8.0f}ms {best_page:8.0f}ms   {plan[:110]}")
    sys.stdout.flush()
