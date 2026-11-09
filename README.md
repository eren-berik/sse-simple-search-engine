# Content Search Service

Farklı formatlardaki (JSON, XML) içerik sağlayıcılardan veri toplayan, bu verileri standart bir formata dönüştürüp ortak bir formülle puanlayan ve anahtar kelime araması, tür filtresi, popülerlik/alakalılık sıralaması ve sayfalama sunan bir arama API'si. Üzerinde basit bir web dashboard'u da bulunur.

- **Dil / Framework:** Go 1.26, Fiber v2
- **Veritabanı:** PostgreSQL 16 (full-text search + pg_trgm)
- **Cache:** Redis 7 (opsiyonel, yoksa in-process cache'e düşer)
- **Mimari:** Modüler monolith, katmanlı (handler → service → repository) ve interface'lerle ayrılmış

---

## Hızlı Başlangıç

Gereksinim: Docker (Compose v2).

```bash
make up          # ya da: docker compose up --build -d
```

| Adres | Açıklama |
|---|---|
| http://localhost:8080 | Dashboard |
| http://localhost:8080/docs | Swagger UI (OpenAPI 3) |
| http://localhost:8080/api/v1/contents | API |
| http://localhost:8080/readyz | Hazırlık kontrolü |

Uygulama açılışta migration'ları uygular, iki provider'dan veriyi çeker ve ardından `SYNC_INTERVAL` aralığıyla (varsayılan 10 dk) senkronizasyonu tekrarlar.

```bash
curl 'localhost:8080/api/v1/contents?q=go&type=video&sort=relevance&page=1&per_page=10'
curl  localhost:8080/api/v1/contents/1                   # skor kırılımı ile detay
curl -X POST -H 'X-API-Key: dev-admin-key' localhost:8080/api/v1/admin/sync
```

Durdurmak için `make down` (volume'leri de siler).

### Lokal geliştirme

```bash
cp .env.example .env
make deps               # sadece PostgreSQL (host:5433) ve Redis (host:6379)
make run                # go run ./cmd/server
make test               # unit testler
make test-integration   # unit + gerçek PostgreSQL/Redis ile integration testler
make lint               # go vet + golangci-lint
```

> Postgres host'ta **5433** portundan açılır, böylece lokalde çalışan bir Postgres ile çakışmaz. Portlar `POSTGRES_PORT`, `REDIS_PORT` ve `APP_PORT` ile değiştirilebilir.

---

## Mimari

```
                 ┌──────────────────────── Monolith (tek binary) ───────────────────────┐
                 │                                                                      │
  Browser ──────►│  Fiber router ─ middleware (request-id, log, recover, rate limit)    │
  (dashboard)    │       │                                                              │
                 │       ├── ContentHandler ──► search.Service ──► cache (Redis) ───────┼──► Redis
  API client ───►│       │                          │  cache-aside + singleflight       │
                 │       │                          └──► ContentSearcher (Postgres) ────┼──► PostgreSQL
                 │       └── SyncHandler ──► ingest.Service ──► Store (Postgres) ───────┤
                 │                              │ ▲ ticker (SYNC_INTERVAL)              │
                 │                              ▼                                       │
                 │                    []provider.Provider                               │
                 │                    ├─ jsonprovider ─┐                                │
                 │                    └─ xmlprovider ──┴─ rate-limited HTTP client ─────┼──► Provider API'leri
                 └──────────────────────────────────────────────────────────────────────┘
```

```
cmd/server/
  main.go                   giriş noktası: config, logger, sinyal yönetimi
  boot.go                   bağımlılıkların kurulumu (DB, cache, servisler, router)
  server.go                 HTTP sunucusu + arka plan sync, graceful shutdown
openapi/                    openapi.yaml (binary'ye gömülü, /openapi.yaml)
internal/                   uygulamaya özel kod (domain'e bağımlı)
  config/                   env tabanlı konfigürasyon
  domain/                   Content, SearchQuery, SyncRun, domain hataları (bağımlılık yok)
  scoring/                  puanlama formülü (saf fonksiyon, enjekte edilebilir saat)
  provider/                 Provider interface'i ve normalizasyon yardımcıları
    jsonprovider/           Provider 1: DTO + mapper
    xmlprovider/            Provider 2: DTO + mapper
  ingest/                   senkronizasyon: çek → puanla → upsert → cache invalidate
  repository/postgres/      pgx repository + gömülü goose migration'ları
  service/search/           arama use case'i: validasyon, cache-aside, singleflight
  http/                     router, handler'lar, middleware, JSON response envelope'ları
  web/static/               dashboard (HTML + vanilla JS, binary'ye gömülü)
pkg/                        domain'den bağımsız, yeniden kullanılabilir paketler
  cache/                    Cache interface'i: Redis, Memory ve Noop implementasyonları
  httpclient/               rate limit + retry/backoff yapan outbound HTTP client
test/testdata/              gerçek provider yanıtlarından fixture'lar
```

`pkg/` altındaki paketler `internal/`'dan hiçbir şey import etmez, başka projelerde de kullanılabilir. Domain tiplerine veya hatalarına bağlı olan kod (`response`, `middleware`, provider normalizasyonu) `internal/` altında kalır.

**Temel kararlar:**

- **API, provider'ları senkron çağırmaz.** Veri arka planda çekilip veritabanına yazılır. Arama istekleri sadece DB'ye (ve cache'e) gider. Bir provider düşse bile arama çalışmaya devam eder, provider rate limit'leri de kullanıcı trafiğinden bağımsız kalır.
- **Bağımlılıklar interface ile tersine çevrilir.** Servisler `ContentSearcher`, `Store`, `Cache` ve `Provider` gibi interface'lere bağlıdır. Bu sayede her katman sahte implementasyonlarla izole test edilebilir. Örneğin arama altyapısı ileride Elasticsearch'e taşınırsa servis katmanı değişmez.
- **Monolith, ama modüler.** Case'in ölçeği için ayrı servisler gereksiz operasyonel yük getirir. Paketler, ihtiyaç olursa ingest worker'ının ayrı bir process'e ayrılabileceği şekilde bölündü.

---

## Teknoloji Tercihleri ve Gerekçeleri

### Go + Fiber
Go'nun eşzamanlılık modeli provider'ları paralel çekmeye, tek statik binary üretmesi de küçük bir distroless imaja çok uygun. Fiber fasthttp üzerinde çalışıyor, düşük gecikme sunuyor ve gereken middleware'leri (rate limiter, request-id, recover) hazır getiriyor. fasthttp request buffer'larını yeniden kullandığı için `Immutable: true` açık. Böylece handler'dan dışarı çıkan string'ler (cache key'leri, singleflight) güvenli kalıyor.

### PostgreSQL (MongoDB ve Elasticsearch yerine)

| İhtiyaç | PostgreSQL ile karşılanma şekli |
|---|---|
| Kalıcı veri tutarlılığı | ACID transaction, `UNIQUE (provider, external_id)`, `CHECK` kısıtları. Provider batch'i tek transaction'da upsert edilir |
| Anahtar kelime araması | Başlık (ağırlık A) ve etiketler (ağırlık B) üzerinde `tsvector` generated column ve GIN index |
| Alakalılık skoru | `ts_rank` ile `pg_trgm similarity` toplamı |
| Kısmi eşleşme ("kubern" → Kubernetes) | `pg_trgm` GIN index ile `ILIKE` |
| Popülerliğe göre sıralama | Önceden hesaplanmış `score` kolonu ve `(type, score DESC)` index'i |
| Farklı provider formatları | Mapper'lar standart formata çevirir. Orijinal kayıt `raw_payload JSONB`'de saklanır |

- **MongoDB:** Şema esnekliği bu projede belirleyici değil, çünkü farklılıklar zaten mapper katmanında normalize ediliyor. Buna karşılık Mongo'nun text search'ü koleksiyon başına tek index'le sınırlı ve önek/kısmi eşleşmeyi desteklemiyor (gelişmiş arama Atlas Search'e bağlı, lokalde çalışmıyor). Çok dokümanlı transaction için de replica set gerekiyor.
- **Elasticsearch:** Bu veri hacminde Postgres FTS yeterli. ES eklemek ikinci bir veri deposu, senkronizasyon (dual-write/outbox) ve ciddi bir bellek yükü demek. Veri milyonlara çıkarsa, ya da typo toleransı, Türkçe analyzer veya facet gibi ihtiyaçlar doğarsa, ES okuma tarafı için bir read model olarak eklenebilir. Postgres kaynak veri olarak kalır ve `ContentSearcher` arkasında servis katmanı değişmez.

### Redis
Arama sonuçları için paylaşımlı cache ve instance'lar arası paylaşımlı rate limit sayaçları. `REDIS_URL` verilmezse uygulama in-process cache ile çalışır.

### Diğer
`pgx/v5` (performanslı native PG driver), `goose` (binary'ye gömülü migration'lar, açılışta otomatik uygulanır), `golang.org/x/time/rate` (provider başına token bucket), `log/slog` (yapısal JSON log), `testify`.

---

## Provider Entegrasyonu

| | Provider 1 (JSON) | Provider 2 (XML) |
|---|---|---|
| Başlık | `title` | `headline` |
| Metrikler | `metrics{views, likes, duration}` | `stats{views, likes, duration \| reading_time, reactions, comments}` |
| Tür | `video` | `video`, `article` |
| Tarih | RFC3339 | `YYYY-MM-DD` |
| Etiketler | `tags[]` | `categories>category` |

Normalizasyon kuralları:
- `article` → `text`
- Süre `mm:ss` / `hh:mm:ss` formatından saniyeye çevrilir
- Tarihler UTC'ye çevrilir
- Etiketler küçük harfe çevrilir ve tekilleştirilir

Geçersiz kayıtlar (eksik id/başlık/tarih, bilinmeyen tür, negatif metrik) loglanıp **atlanır**. Böylece tek bir bozuk kayıt tüm feed'i durdurmaz.

**ID çakışması:** İki provider da `v1`, `v2`, `v3` ID'lerini kullanıyor. Bu yüzden tekillik `(provider, external_id)` çifti üzerinden sağlanıyor.

**İstek limiti yönetimi:** Her provider'ın kendi HTTP client'ı ve kendi token bucket'ı var (`PROVIDER_RATE_PER_SEC`, `PROVIDER_BURST`). Ağ hataları, `429` ve `5xx` yanıtları exponential backoff ile yeniden denenir (`PROVIDER_MAX_RETRIES`). `429` geldiğinde `Retry-After` header'ına uyulur. Yanıt boyutu 10 MB ile sınırlı ve her denemenin kendi timeout'u var.

**Hata izolasyonu:** Provider'lar paralel çekilir. Biri başarısız olsa bile diğerinin verisi yazılır. Her çalışmanın sonucu `sync_runs` tablosuna kaydedilir (`GET /api/v1/sync/runs`). Aynı anda tek bir sync çalışabilir; manuel tetikleme çakışırsa `409` döner.

### Yeni provider ekleme

1. `internal/provider/<ad>provider/` altında wire format için bir DTO ve `domain.Content`'e mapper yazın. Ortak yardımcılar hazır: `provider.ParseType`, `ParseDate`, `ParseDuration`, `CleanTags`, `Validate`.
2. `provider.Provider` interface'ini (`Name()`, `Fetch(ctx)`) implemente edin.
3. `cmd/server/boot.go` içindeki `buildProviders` listesine ekleyin.

Puanlama, kalıcılık, cache ve API tarafında değişiklik gerekmez.

---

## Puanlama

```
Final Skor = (Temel Puan × İçerik Türü Katsayısı) + Güncellik Puanı + Etkileşim Puanı
```

| | Video | Metin |
|---|---|---|
| Temel puan | `views/1000 + likes/100` | `reading_time + reactions/50` |
| Katsayı | 1.5 (`SCORE_VIDEO_COEFFICIENT`) | 1.0 (`SCORE_TEXT_COEFFICIENT`) |
| Etkileşim | `(likes/views) × 10` | `(reactions/reading_time) × 5` |
| Güncellik | ≤7 gün +5, ≤30 gün +3, ≤90 gün +1, daha eski 0 | aynı |

- Skor ingest sırasında hesaplanır ve `score` kolonunda saklanır, böylece sıralama index üzerinden yapılır.
- Güncellik puanı zamana bağlı olduğu için her sync'te, provider'ların artık döndürmediği kayıtların skorları da yeniden hesaplanır.
- Sıfıra bölme korunur: `views = 0` veya `reading_time = 0` ise etkileşim puanı 0 olur.
- `GET /api/v1/contents/{id}` skor kırılımını (base, katsayı, güncellik, etkileşim) döndürür. Dashboard'da bir satıra tıklayınca görünür.

Örnek: provider1/v1 → `(15 + 12) × 1.5 + 0 + (1200/15000) × 10 = 41.3`

**Formülle ilgili gözlemler** (formül case'te tanımlandığı gibi, değiştirilmeden uygulandı):
- Metin içeriğin etkileşim puanı (`reactions/reading_time × 5`) diğer bileşenlerden çok daha büyük ölçekte. Örnek veride tek makale 298 puanla videoların çok önünde (videolar 33–70 arası). Gerçek bir sistemde bileşenlerin normalize edilmesi (örn. log ölçeği veya min-max) düşünülmeli. Katsayılar zaten env üzerinden ayarlanabiliyor.
- Mock verideki tüm tarihler 2024'e ait. Bu yüzden bugün itibarıyla güncellik puanı 0. Testler sabit bir saat enjekte ederek tüm güncellik eşiklerini (7/30/90 gün sınırları dahil) doğruluyor.

---

## Arama ve Sıralama

`GET /api/v1/contents?q=&type=video|text&sort=popularity|relevance&page=1&per_page=20`

- **Anahtar kelime:** Başlık ve etiketler üzerinde full-text eşleşme **veya** başlıkta substring eşleşmesi. `websearch_to_tsquery` sayesinde `"tam ifade"` ve `-hariç` sözdizimi desteklenir. Kullanıcı girdisindeki LIKE wildcard'ları (`%`, `_`) escape edilir.
- **Sıralama:**
  - `popularity`: `score DESC`
  - `relevance`: `ts_rank + similarity DESC`, eşitlikte `score`
  - `sort` verilmezse: keyword varsa relevance, yoksa popularity. Keyword olmadan istenen relevance, popularity'ye düşer.
- **Sayfalama:** offset/limit. Yanıtta `meta{page, per_page, total, total_pages, sort}` döner. `per_page` en fazla 100.
- **Hatalar:** Tutarlı bir zarf kullanılır: `{"error": {"code": "invalid_input", "message": "..."}}`. 400, 401, 403, 404, 409, 429 ve 500 durumları için ayrı kodlar var. 500'lerde iç hata detayı istemciye sızdırılmaz.

Tam şema için [`openapi/openapi.yaml`](openapi/openapi.yaml) ya da `/docs`.

---

## Cache Stratejisi

- **Cache-aside:** Arama sonuçları normalize edilmiş sorgunun hash'iyle saklanır: `search:v{version}:{sha256(q|type|sort|page|per_page)}`, TTL `CACHE_TTL` (90 sn). Yanıttaki `X-Cache` header'ı `HIT`, `MISS` veya `BYPASS` değerini taşır.
- **Versiyonlu invalidation:** Sync bittiğinde `search:version` sayacı `INCR` edilir. Eski key'ler bir daha okunmaz ve TTL dolunca silinir. `SCAN`/`DEL` gerekmez, invalidation O(1) ve atomik.
- **Stampede koruması:** Aynı key için eşzamanlı cache miss'ler `singleflight` ile tek bir DB sorgusuna indirgenir.
- **Graceful degradation:** Redis erişilemezse istekler doğrudan DB'den karşılanır (`BYPASS`). `/readyz` bu durumda `degraded` döner ama servis hazır kalır. Rate limiter da Redis yoksa fail-open çalışır.
- Provider yanıtları cache'lenmez, çünkü kalıcı kopya zaten veritabanında.

---

## Testler

```bash
make test               # unit (DB gerektirmez)
make test-integration   # + PostgreSQL ve Redis üzerinde integration testleri
```

| Katman | Ne test ediliyor |
|---|---|
| `scoring` | Formül bileşenleri, güncellik eşik sınırları, sıfıra bölme, özel katsayılar (table-driven, sabit saat) |
| `provider` | Normalizasyon (tür, süre, tarih, etiket) |
| `pkg/httpclient` | Retry, 4xx'te retry yapmama, `Retry-After`, rate limit, context iptali (`httptest`) |
| `jsonprovider` / `xmlprovider` | Gerçek fixture'ların parse edilmesi, geçersiz kayıtların atlanması, bozuk payload |
| `service/search` | Cache hit/miss, invalidation, Redis arızasında bypass, hataların cache'lenmemesi, singleflight |
| `ingest` | Puanlama + upsert, provider hata izolasyonu, eşzamanlı sync reddi |
| `http/router` | Fiber `app.Test` ile uçtan uca: validasyon, hata zarfı, API key, rate limit, readiness, statik dosyalar |
| `repository/postgres` *(integration)* | Upsert idempotency, ID çakışması, FTS/relevance/substring/tag araması, wildcard escape, sayfalama, skor yeniden hesaplama, sync_runs |
| `pkg/cache` | Memory cache TTL ve sayaç; *(integration)* Redis get/set/TTL/incr |

Integration testleri her çalıştırmada geçici bir veritabanı oluşturup sonunda siler, uygulama verisine dokunmaz. CI (GitHub Actions) vet, golangci-lint ve tüm testleri Postgres/Redis servisleriyle çalıştırır.

---

## Konfigürasyon

Tüm ayarlar environment variable ile verilir. Varsayılanlar için [`.env.example`](.env.example) dosyasına bakın. Öne çıkanlar:

| Değişken | Varsayılan | Açıklama |
|---|---|---|
| `DATABASE_URL` | *(zorunlu)* | PostgreSQL bağlantısı |
| `REDIS_URL` | boş | Boşsa in-process cache kullanılır |
| `CACHE_TTL` | `90s` | Arama cache süresi |
| `SYNC_INTERVAL` | `10m` | Periyodik senkronizasyon aralığı |
| `PROVIDER_RATE_PER_SEC` / `PROVIDER_BURST` | `2` / `1` | Provider başına istek limiti |
| `PROVIDER_MAX_RETRIES` / `PROVIDER_TIMEOUT` | `3` / `5s` | Retry ve deneme başına timeout |
| `RATE_LIMIT_MAX` / `RATE_LIMIT_WINDOW` | `100` / `1m` | API için IP başına limit |
| `ADMIN_API_KEY` | boş | Boşsa admin endpoint'leri kapalıdır |

---

## Bilinen Sınırlamalar ve Geliştirme Fikirleri

- **Provider sayfalama:** Mock yanıtlar sayfalama metadata'sı içeriyor (`total: 150`, `total_count: 75`), ancak statik dosya oldukları için tüm sayfalar aynı veriyi dönüyor. Bu yüzden şimdilik tek sayfa çekiliyor. Gerçek bir API'de `Fetch` sayfa sayfa gezinecek şekilde genişletilebilir.
- **Offset pagination:** Büyük veri setlerinde derin sayfalar pahalı olur. `(score, id)` üzerinden keyset (cursor) pagination'a geçilebilir.
- **Skor normalizasyonu:** Yukarıda anlatıldığı gibi, bileşenlerin ölçek farkı.
- **Gözlemlenebilirlik:** Prometheus metrikleri (istek süresi, cache hit oranı, sync süresi) ve OpenTelemetry tracing eklenebilir.
- **Ölçeklenme:** Ingest ayrı bir worker process'ine ayrılabilir. Birden fazla instance'ta sync'in tek kopyada çalışması için Postgres advisory lock kullanılabilir. Arama yükü büyürse Elasticsearch/OpenSearch read model olarak eklenebilir.
- **Admin auth:** Sabit API key yerine gerçek bir kimlik doğrulama (JWT/OIDC).
