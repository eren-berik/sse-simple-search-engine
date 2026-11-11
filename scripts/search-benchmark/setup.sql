-- Builds bench.contents: 1,000,000 synthetic rows with the same columns, generated search vector
-- and indexes as public.contents, in a separate schema so the real table (and its id sequence)
-- is not touched. Takes about 4 minutes on a laptop; drop it with: DROP SCHEMA bench CASCADE;
--
-- Word frequencies are skewed on purpose: arr[1 + floor(n * random()^3)] makes the first words
-- very common ("go" ends up in ~28% of rows, "docker" in ~9%) and most words rare, like real
-- titles. Parallel workers are off because Docker's default /dev/shm (64 MB) is too small for them.
SET max_parallel_workers_per_gather = 0;
SET max_parallel_maintenance_workers = 0;
SET maintenance_work_mem = '256MB';

DROP SCHEMA IF EXISTS bench CASCADE;
CREATE SCHEMA bench;
CREATE TABLE bench.providers (name TEXT PRIMARY KEY, enabled BOOLEAN NOT NULL);
INSERT INTO bench.providers VALUES ('p1', true), ('p2', true), ('p3', true), ('p4', false);
CREATE TABLE bench.contents (LIKE public.contents INCLUDING DEFAULTS INCLUDING GENERATED INCLUDING CONSTRAINTS);
ALTER TABLE bench.contents ALTER COLUMN id DROP DEFAULT;

CREATE TABLE bench.vocab AS SELECT ARRAY[
 'go','docker','kubernetes','programming','tutorial','advanced','introduction','guide','patterns','testing',
 'api','rest','architecture','clean','concurrency','pipeline','devops','cloud','database','performance',
 'security','design','microservices','python','java','rust','web','frontend','backend','data',
 'machine','learning','deep','network','linux','git','ci','cd','monitoring','scaling']
 || ARRAY(SELECT 'w' || g FROM generate_series(1, 5000) g) AS arr;

INSERT INTO bench.contents (id, provider, external_id, title, type, views, likes, duration_sec,
  reading_time, reactions, comments, tags, published_at, score, score_updated_at)
SELECT g,
  'p' || (1 + g % 4),
  'x' || g,
  (SELECT string_agg(v.arr[1 + floor(5039 * random()^3)::int + s * 0], ' ') FROM generate_series(1, 3 + (g % 5)) s),
  CASE WHEN g % 3 = 0 THEN 'text' ELSE 'video' END,
  (random() * 100000)::bigint, (random() * 5000)::bigint, (random() * 3600)::int,
  (random() * 30)::int, (random() * 2000)::bigint, (random() * 300)::bigint,
  ARRAY(SELECT v.arr[1 + floor(5039 * random()^2)::int + s * 0] FROM generate_series(1, 2 + g % 2) s),
  now() - (random() * interval '730 days'),
  random() * 500, now()
FROM generate_series(1, 1000000) g CROSS JOIN bench.vocab v;

CREATE INDEX ON bench.contents USING GIN (search_vector);
CREATE INDEX ON bench.contents USING GIN (title gin_trgm_ops);
CREATE INDEX ON bench.contents (score DESC, id);
CREATE INDEX ON bench.contents (type, score DESC, id);
CREATE INDEX ON bench.contents USING GIN (tags);
ALTER TABLE bench.contents ADD PRIMARY KEY (id);
VACUUM ANALYZE bench.contents;
