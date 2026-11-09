# Content Search Service

Farklı formatlardaki (JSON, XML) içerik sağlayıcılardan veri toplayan, bu verileri standart bir formata dönüştürüp ortak bir formülle puanlayan bir arama sistemi. Anahtar kelime araması, tür ve provider filtresi, popülerlik/alakalılık sıralaması ve sayfalama sunar. Kullanıcılar kayıt olup giriş yapar. Yetki düzeyine göre viewer içerikleri görüntüleyip filtreler; admin ise ek olarak provider ekler ve senkronize eder.

Sistem üç mikroservisten oluşur:

| Servis | Sorumluluk |
|---|---|
| **sse-gateway** | Tek giriş noktası. API isteklerini ilgili servise yönlendirir; rate limit, request-id, dashboard ve API dokümanı burada |
| **iam-service** | Kullanıcılar, şifreler, roller, access/refresh token'lar |
| **sse-service** | Asıl iş mantığı: provider entegrasyonu, puanlama, arama, cache, senkronizasyon |

- **Dil / Framework:** Go 1.26, Fiber v2
- **Veritabanı:** PostgreSQL 16 (servis başına ayrı veritabanı)
- **Cache:** Redis 7 (opsiyonel; yoksa servisler in-process çalışır)
- **Kimlik doğrulama:** JWT (RS256) access token, JWKS ile dağıtılan public key ve rotasyonlu refresh token

---

## Hızlı Başlangıç

Gereksinim: Docker (Compose v2).

```bash
make up          # ya da: docker compose up --build -d
```

| Adres | Açıklama |
|---|---|
| http://localhost:8080 | Dashboard (kayıt ol / giriş yap) |
| http://localhost:8080/docs | Swagger UI (OpenAPI 3) |
| http://localhost:8080/api/v1/... | API (gateway üzerinden) |
| http://localhost:8080/readyz | Gateway ve arkasındaki servislerin hazırlık durumu |

Dışarıya yalnızca gateway açılır. iam-service ve sse-service sadece compose ağı içinden erişilebilir.

```bash
# Admin olarak kayıt ol ve token al
TOKEN=$(curl -s -X POST localhost:8080/api/v1/auth/register -H 'Content-Type: application/json' \
  -d '{"username":"alice","password":"s3cret-pass","role":"admin"}' | sed -n 's/.*"access_token":"\([^"]*\)".*/\1/p')

curl -H "Authorization: Bearer $TOKEN" 'localhost:8080/api/v1/contents?q=go&type=video&provider=provider1'
curl -H "Authorization: Bearer $TOKEN" -X POST localhost:8080/api/v1/providers -H 'Content-Type: application/json' \
  -d '{"name":"my-feed","url":"https://example.com/feed.xml","format":"xml"}'
curl -H "Authorization: Bearer $TOKEN" -X POST localhost:8080/api/v1/sync
```

Durdurmak için `make down` kullanılır (volume'leri de siler).

**Kalıcı imzalama anahtarı (opsiyonel):** `make keys` bir RSA anahtarı üretir. `.env` dosyasına `JWT_PRIVATE_KEY_FILE=/keys/jwt.pem` eklendiğinde iam-service yeniden başlasa bile verilmiş access token'lar geçerli kalır. Anahtar verilmezse her açılışta yeni bir anahtar üretilir; dashboard bu durumda token'ı refresh ederek devam eder.

### Lokal geliştirme

```bash
make deps               # sadece PostgreSQL (host:5433) ve Redis (host:6379)
make test               # tüm modüllerin unit testleri
make test-integration   # + gerçek PostgreSQL/Redis ile integration testler
make lint               # go vet + golangci-lint (her modül)
```

Postgres host'ta **5433** portundan açılır, böylece lokalde çalışan bir Postgres ile çakışmaz.

---

## Mimari

```
                        ┌──────────────────────── sse-gateway (:8080, tek açık port) ────────────────────────┐
  Browser / API ───────►│ request-id · log · rate limit (IP başına; login/register/refresh için daha sıkı)    │
                        │ dashboard (statik) · /docs · /openapi.yaml                                          │
                        │   /api/v1/auth/*                         /api/v1/contents|providers|sync/*          │
                        └───────────┬───────────────────────────────────────────┬────────────────────────────┘
                                    ▼                                           ▼
                  ┌──────── iam-service ────────┐            ┌──────────────── sse-service ────────────────┐
                  │ register · login · refresh  │   JWKS     │ JWT doğrulama + rol kontrolü (viewer/admin) │
                  │ logout · me                 │◄───────────│ arama (cache-aside) · provider yönetimi      │
                  │ RS256 imzalama (private key)│  public    │ ingest/sync (periyodik + manuel)             │
                  └──────────────┬──────────────┘  keys      └──────┬────────────────┬─────────────┬────────┘
                                 ▼                                  ▼                ▼             ▼
                          PostgreSQL: iam                   PostgreSQL: contents    Redis     Provider API'leri
                                                                                          (JSON / XML)
```

### Temel kararlar

- **Gateway iş mantığı içermez.** Sadece route tablosuna göre yönlendirir ve edge görevlerini üstlenir: rate limit, `X-Forwarded-*` header'ları, request-id ve statik dosyalar. Tanımsız API path'leri hiçbir servise iletilmez.
- **Token'ı servisler doğrular.** iam-service access token'ları private key ile imzalar ve public key'leri `/.well-known/jwks.json` adresinde yayınlar. sse-service bu key'leri çekip cache'ler ve her isteği kendisi doğrular. Rol kontrolü iş kurallarının sahibi olan sse-service'te yapılır. Gateway token'ı olduğu gibi iletir; böylece gateway'i atlayan bir istek de yetkisiz işlem yapamaz.
- **Servisler birbirinden bağımsızdır.** Her servis ayrı bir Go modülüdür ve kendi `pkg/` klasörü, kendi Dockerfile'ı ve kendi build context'i vardır. Ortak bir kütüphane modülü yoktur. Bu, `httpx` ve `jwtauth` gibi küçük yardımcıların her serviste ayrı bir kopyası olması anlamına gelir; karşılığında servisler bağımsız versiyonlanıp deploy edilebilir. Kökteki `go.work` sadece editör kolaylığı içindir; Docker build'leri onu kullanmaz.
- **Her servisin kendi veritabanı vardır.** Aynı PostgreSQL instance'ı üzerinde `iam` ve `contents` adında iki ayrı veritabanı kullanılır; servisler birbirinin tablolarına erişmez.
- **API, provider'ları senkron çağırmaz.** Provider verisi arka planda çekilip veritabanına yazılır, arama istekleri sadece veritabanına ve cache'e gider.

### Klasör yapısı

```
WingieCase/
├── sse-gateway/
│   ├── cmd/server/              main.go · boot.go (bağımlılıkların kurulumu) · server.go (sunucu, graceful shutdown)
│   ├── internal/
│   │   ├── config/
│   │   ├── proxy/               route tablosu, upstream'e iletme (Forward/Mount)
│   │   ├── http/router/
│   │   └── web/static/          dashboard (HTML + vanilla JS, binary'ye gömülü)
│   ├── openapi/                 tüm sistemin OpenAPI spec'i
│   └── pkg/                     httpx, ratelimit
├── iam-service/
│   ├── cmd/server/
│   ├── internal/
│   │   ├── config/  domain/
│   │   ├── auth/                register/login/refresh/logout, bcrypt, token rotasyonu (+ authtest: in-memory store)
│   │   ├── repository/postgres/ users, refresh_tokens + migration'lar
│   │   └── http/{handler,router}/
│   └── pkg/                     httpx, jwtauth (signer + verifier + JWKS + middleware)
├── sse-service/
│   ├── cmd/server/
│   ├── internal/
│   │   ├── config/  domain/  scoring/
│   │   ├── provider/            Provider interface'i, normalizasyon, jsonprovider, xmlprovider, registry
│   │   ├── ingest/              senkronizasyon
│   │   ├── repository/postgres/ contents, providers, sync_runs + migration'lar
│   │   ├── service/{search,providers}/
│   │   └── http/{handler,router}/
│   ├── pkg/                     cache, httpclient, httpx, jwtauth (sadece doğrulama + jwtauthtest)
│   └── test/testdata/           gerçek provider yanıtlarından fixture'lar
├── deploy/                      postgres init script'i, (opsiyonel) imzalama anahtarı
├── docker-compose.yml · Makefile · go.work · .golangci.yml · .github/workflows/ci.yml
```

Her serviste aynı katmanlı yapı kullanılır: handler → service → repository. Servisler interface'lere bağlıdır (`ContentSearcher`, `Store`, `ProviderSource`, `Builder`, `Cache`, `Authenticator`), bu sayede her katman sahte implementasyonlarla izole test edilebilir.

---

## Kimlik Doğrulama ve Yetkilendirme

### Akış

1. **Register** (`POST /api/v1/auth/register`): Kullanıcı adı, şifre ve rol (`viewer` varsayılan, ya da `admin`) gönderilir. Yanıtta kullanıcı bilgisiyle birlikte `access_token` ve `refresh_token` döner.
2. **Login** (`POST /api/v1/auth/login`): Aynı yanıt döner. Her login yeni bir oturum (refresh token ailesi) başlatır.
3. API çağrıları `Authorization: Bearer <access_token>` header'ı ile yapılır.
4. Access token'ın süresi dolduğunda (15 dk) `POST /api/v1/auth/refresh` çağrılır. Gönderilen refresh token iptal edilir, yerine yeni bir çift verilir.
5. **Logout** (`POST /api/v1/auth/logout`): O oturuma ait tüm refresh token'ları iptal eder.

### Token tasarımı

| | Access token | Refresh token |
|---|---|---|
| Biçim | JWT, **RS256** imzalı. Claim'ler: `sub` (user id), `username`, `role`, `iss`, `aud`, `exp`, `jti` | 256 bit rastgele, opaque |
| Ömür | 15 dakika (`ACCESS_TOKEN_TTL`) | 7 gün (`REFRESH_TOKEN_TTL`) |
| Saklama | Saklanmaz, imza ile doğrulanır | DB'de sadece **SHA-256 hash**'i tutulur |
| Doğrulayan | Her servis, JWKS'ten aldığı public key ile | Sadece iam-service |

- **Neden RS256 ve JWKS:** Simetrik bir imzada (HS256) token'ı doğrulayan her servis aynı zamanda token üretebilirdi. Asimetrik imzada private key yalnızca iam-service'te durur; diğer servisler sadece doğrulayabilir. `kid` header'ı sayesinde key rotasyonu yapılabilir: bilinmeyen bir `kid` görüldüğünde JWKS yeniden çekilir. Bu yeniden çekme rate limitlidir, böylece rastgele `kid`'li token'larla iam-service'e yük bindirilemez. iam-service erişilemezse sse-service bunu "geçersiz token" değil `503 auth_unavailable` olarak bildirir.
- **Refresh token rotasyonu ve tekrar kullanım tespiti:** Her refresh işleminde eski token tek bir koşullu `UPDATE` ile iptal edilir; böylece aynı token'la eşzamanlı yapılan iki refresh'ten yalnızca biri başarılı olur. Daha önce kullanılmış bir token tekrar gelirse, token'ın çalındığı varsayılır ve o oturumdaki **tüm** token'lar iptal edilir.
- **Şifreler:** bcrypt ile saklanır (cost 12, `BCRYPT_COST`). Şifre uzunluğu 8–72 byte arasında olmalı; bcrypt 72 byte'tan sonrasını yok saydığı için üst sınır bu. Var olmayan bir kullanıcı adıyla login denendiğinde de sahte bir hash ile karşılaştırma yapılır. Böylece yanıt süresinden hangi kullanıcı adlarının kayıtlı olduğu anlaşılamaz.
- **Kullanıcı adları:** Küçük harfe çevrilerek saklanır ve büyük/küçük harf duyarsız olarak tekildir.

### Roller

| İşlem | viewer | admin |
|---|:-:|:-:|
| İçerik arama, filtreleme, detay (`GET /contents`, `/contents/{id}`) | ✓ | ✓ |
| Provider listesi, son sync durumları (`GET /providers`, `/sync/runs`) | ✓ | ✓ |
| Provider ekleme / güncelleme (`POST /providers`, `PATCH /providers/{id}`) | | ✓ |
| Sync tetikleme (`POST /sync`, `POST /providers/{id}/sync`) | | ✓ |

**Rol tipi:** Go'da string tabanlı bir enum (`type Role string`, sabitler `RoleAdmin`/`RoleViewer`), veritabanında `TEXT` ve `CHECK (role IN ('admin','viewer'))` olarak tutuluyor.
- Integer'a göre JWT'de, JSON'da ve DB'de okunabilir, numaralandırma derdi yok.
- PostgreSQL `ENUM` tipine göre yeni bir rol eklemek tek satırlık bir `CHECK` değişikliği; ENUM'dan değer silmek ise zor.
- Geçersiz değerler hem uygulamada (`ParseRole`) hem DB'de reddediliyor.

> ⚠️ **Rol seçimi bilinçli olarak serbest.** İstendiği gibi register isteğinde rol serbestçe seçilebiliyor; yani herkes kendini admin olarak kaydedebilir. Bu demo için kabul edilebilir, ama gerçek bir sistemde public register'ın her zaman `viewer` oluşturması, admin yetkisinin ise ayrı bir kanaldan verilmesi gerekir (davet kodu, mevcut bir admin'in onayı ya da başlangıçta oluşturulan bir admin hesabı).

### Dashboard'da oturum

Token'lar `sessionStorage`'da tutulur: sayfa yenilense de kalır, sekme kapanınca silinir. Bir istek 401 döndüğünde access token sessizce refresh edilip istek tekrarlanır. Aynı anda gelen 401'ler tek bir refresh isteğinde birleştirilir; böylece rotasyona uğramış bir refresh token asla iki kez gönderilmez (gönderilseydi tekrar kullanım tespiti tüm oturumu kapatırdı). Admin'ler "Provider'lar" sekmesini görür; viewer'a bu sekme hiç gösterilmez, sunucu tarafında da reddedilir.

> Bilinen ödünleşim: `sessionStorage` bir XSS açığında JavaScript tarafından okunabilir. Dashboard tüm veriyi `textContent` ile basıyor ve harici script yüklemiyor. Üretimde refresh token'ı `HttpOnly` + `SameSite=Strict` bir cookie'ye taşımak daha güvenli olur.

---

## Teknoloji Tercihleri ve Gerekçeleri

### Go + Fiber
- **Go:** Eşzamanlılık modeli provider'ları paralel çekmeye uygun. Tek statik binary üretmesi de servis başına küçük bir distroless imaj demek.
- **Fiber:** fasthttp üzerinde çalışıyor ve gereken parçaları hazır getiriyor: rate limiter, request-id, recover, reverse proxy.
- **`Immutable: true`:** fasthttp request buffer'larını yeniden kullandığı için açık. Böylece handler'dan dışarı çıkan string'ler güvenli kalıyor (cache key'leri, singleflight, proxy).

### Neden üç servis
Kimlik yönetimi ile içerik araması farklı hızlarda değişen, farklı güvenlik gereksinimleri olan iki alan:
- **Private key izolasyonu:** İmzalama anahtarı sadece iam-service'te kalıyor.
- **Bağımsız ölçekleme:** Arama yükü arttığında yalnızca sse-service ölçeklenebiliyor.
- **Tek giriş noktası:** Gateway sayesinde istemci tek bir origin görüyor (CORS gerekmiyor). Rate limit gibi edge görevleri de tek yerde toplanıyor.

### PostgreSQL (MongoDB ve Elasticsearch yerine)

| İhtiyaç | PostgreSQL ile karşılanma şekli |
|---|---|
| Kalıcı veri tutarlılığı | ACID transaction'lar; `UNIQUE (provider, external_id)`, `CHECK` ve `contents → providers` foreign key kısıtları. Provider batch'i tek transaction'da upsert edilir |
| Anahtar kelime araması | Başlık (ağırlık A) ve etiketler (ağırlık B) üzerinde `tsvector` generated column + GIN index |
| Alakalılık skoru | `ts_rank` ve `pg_trgm similarity` toplamı |
| Kısmi eşleşme ("kubern" → Kubernetes) | `pg_trgm` GIN index ile `ILIKE` |
| Popülerliğe göre sıralama | Önceden hesaplanmış `score` kolonu + `(type, score DESC)` index'i |
| Token rotasyonu | Tek bir koşullu `UPDATE` ile atomik "tüket" işlemi |

- **MongoDB:** Formatlar arası farklar zaten mapper katmanında normalize ediliyor, bu yüzden şema esnekliği belirleyici değil. Ayrıca Mongo'nun text search'ü koleksiyon başına tek index'le sınırlı ve kısmi eşleşme yapamıyor. Çok dokümanlı transaction için de replica set gerekiyor.
- **Elasticsearch:** Bu veri hacminde Postgres FTS yeterli. Veri milyonlara çıkarsa ES bir read model olarak eklenebilir. Postgres kaynak veri olarak kalır; ES `ContentSearcher` interface'inin arkasına yerleşir ve servis katmanı değişmez.

### Redis
- **sse-service:** Arama sonuçlarının paylaşımlı cache'i.
- **Gateway:** Instance'lar arasında paylaşılan rate limit sayaçları.

Redis ikisi için de opsiyonel; ulaşılamazsa sistem çalışmaya devam eder (aşağıya bakın).

### Diğer
- `pgx/v5`: PostgreSQL driver'ı.
- `goose`: Migration'lar binary'ye gömülü, açılışta otomatik uygulanır.
- `golang-jwt/jwt/v5`: Token imzalama ve doğrulama.
- `x/crypto/bcrypt`: Şifre hash'leme.
- `x/time/rate`: Provider başına token bucket.
- `log/slog`: Yapısal JSON log. Gateway'in ürettiği `X-Request-ID` tüm servislerin loglarında görünür.
- `testify`: Test assertion'ları.

---

## Provider Entegrasyonu

Provider'lar sse-service'in `providers` tablosunda tutulur (`name`, `url`, `format`, `enabled`). Case'teki iki provider migration ile seed edilir. Admin, dashboard'dan ya da API'den yeni provider ekleyebilir, provider'ları etkinleştirip devre dışı bırakabilir ve tek tek ya da toplu sync başlatabilir. Bunlar için kod değişikliği ya da restart gerekmez: her sync, etkin provider'ları DB'den okur.

**Devre dışı bırakma:** Devre dışı bir provider sync edilmez ve içerikleri arama sonuçlarında ve içerik detayında gösterilmez. İçerikler silinmez; provider tekrar etkinleştirildiğinde hemen geri gelir. Etkin/devre dışı değişikliği arama cache'ini anında geçersiz kılar.

**Format = adapter.** `format` alanı payload'ı okuyacak adapter'ı seçer:

| | `json` (provider1 şeması) | `xml` (provider2 şeması) |
|---|---|---|
| Başlık | `title` | `headline` |
| Metrikler | `metrics{views, likes, duration}` | `stats{views, likes, duration \| reading_time, reactions, comments}` |
| Tür | `video` / `text` / `article` | `video` / `text` / `article` |
| Tarih | RFC3339 | `YYYY-MM-DD` |
| Etiketler | `tags[]` | `categories>category` |

Yeni eklenen bir provider, seçtiği formatın şemasına uyan veri döndürmelidir. Tamamen farklı bir şema için yeni bir adapter yazılıp `internal/provider/registry` içinde kaydedilir; o formatı kullanan provider'lar da çalışma anında eklenebilir hale gelir.

**Normalizasyon kuralları:**
- `article` türü `text` olarak eşlenir.
- Süre `mm:ss` / `hh:mm:ss` formatından saniyeye çevrilir.
- Tarihler UTC'ye çevrilir.
- Etiketler küçük harfe çevrilip tekilleştirilir.

Geçersiz kayıtlar loglanıp atlanır.

**ID çakışması:** Tekillik `(provider, external_id)` çifti üzerinden sağlanır, çünkü iki provider da `v1`, `v2`, `v3` ID'lerini kullanıyor.

**İstek limiti yönetimi:**
- Her provider'ın kendi HTTP client'ı ve token bucket'ı var. Client provider başına cache'lenir, URL değişince yenisi oluşturulur.
- Ağ hataları, `429` ve `5xx` yanıtları exponential backoff ile yeniden denenir; `429`'da `Retry-After` header'ına uyulur.
- Yanıt boyutu 10 MB ile sınırlı.

**Hata izolasyonu:**
- Provider'lar paralel çekilir; biri başarısız olsa bile diğerlerinin verisi yazılır.
- Her çalışmanın sonucu `sync_runs` tablosuna kaydedilir.
- Aynı anda tek bir sync çalışabilir; çakışan istek `409 sync_in_progress` alır.
- Devre dışı bir provider tek başına sync edilmek istenirse `400` döner.

> Not: Admin'ler istedikleri URL'yi ekleyebildiği için sse-service bu adreslere istek atar (SSRF yüzeyi). URL şeması `http`/`https` ile sınırlı. Üretimde iç ağ adreslerinin engellenmesi ya da bir allowlist kullanılması önerilir.

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

- Skor ingest sırasında hesaplanıp `score` kolonunda saklanır; sıralama index üzerinden yapılır.
- Güncellik puanı zamana bağlı olduğu için her sync'te, provider'ların artık döndürmediği kayıtların skorları da yeniden hesaplanır.
- Sıfıra bölme korunur.
- `GET /api/v1/contents/{id}` skor kırılımını döndürür. Dashboard'da bir satıra tıklayınca görünür.

Örnek: provider1/v1 → `(15 + 12) × 1.5 + 0 + (1200/15000) × 10 = 41.3`

**Formülle ilgili gözlemler** (formül case'te tanımlandığı gibi, değiştirilmeden uygulandı):
- Metin içeriğin etkileşim puanı diğer bileşenlerden çok daha büyük ölçekte. Örnek veride tek makale 298 puan alırken videolar 33–70 arasında kalıyor. Gerçek bir sistemde bileşenlerin normalize edilmesi düşünülmeli.
- Mock verideki tüm tarihler 2024'e ait, bu yüzden bugün itibarıyla güncellik puanı 0. Testler sabit bir saat enjekte ederek tüm eşikleri doğruluyor.

---

## Arama ve Sıralama

`GET /api/v1/contents?q=&type=video|text&provider=<ad>&sort=popularity|relevance&page=1&per_page=20`

- **Anahtar kelime:** Başlık ve etiketlerde full-text eşleşme **veya** başlıkta substring eşleşmesi. `"tam ifade"` ve `-hariç` sözdizimi desteklenir. Kullanıcı girdisindeki LIKE wildcard'ları (`%`, `_`) escape edilir.
- **Filtreler:** `type` ve `provider` birbiriyle ve anahtar kelimeyle birlikte kullanılabilir.
- **Sıralama:**
  - `popularity`: `score DESC`
  - `relevance`: `ts_rank + similarity DESC`
  - `sort` verilmezse: anahtar kelime varsa relevance, yoksa popularity.
  - Anahtar kelime olmadan istenen relevance, popularity'ye düşer. `meta.sort` hangisinin kullanıldığını bildirir; dashboard da bunu bir uyarıyla gösterir.
- **Sayfalama:** offset/limit. Yanıtta `meta{page, per_page, total, total_pages, sort}` döner.
- **Hatalar:** Tüm servislerde aynı zarf kullanılır: `{"error": {"code": "...", "message": "..."}}`. 500 yanıtlarında iç hata detayı istemciye sızdırılmaz.

Tam şema için [`sse-gateway/openapi/openapi.yaml`](sse-gateway/openapi/openapi.yaml) ya da `/docs`.

---

## Cache Stratejisi

- **Cache-aside:** Key `search:v{version}:{sha256(q|type|provider|sort|page|per_page)}`, TTL 90 sn. Yanıttaki `X-Cache` header'ı `HIT`, `MISS` veya `BYPASS` değerini taşır.
- **Versiyonlu invalidation:** Her sync sonunda `search:version` sayacı `INCR` edilir. Eski key'ler bir daha okunmaz ve TTL dolunca silinir. Invalidation O(1) ve atomik.
- **Stampede koruması:** Aynı key için eşzamanlı cache miss'ler `singleflight` ile tek bir DB sorgusuna indirgenir.
- **Graceful degradation:**
  - Redis yoksa aramalar doğrudan DB'den karşılanır (`BYPASS`).
  - Gateway'deki rate limiter fail-open çalışır: Redis'e ulaşamazsa istekleri reddetmez.
  - JWKS key'leri bellekte cache'lendiği için iam-service kısa süre düşse bile mevcut token'lar doğrulanmaya devam eder.
  - Bu durumlarda `/readyz` `degraded` döner ama servis hazır kalır.

---

## Testler

```bash
make test               # unit testler (DB gerektirmez)
make test-integration   # + PostgreSQL ve Redis üzerinde integration testler
```

| Servis | Katman | Ne test ediliyor |
|---|---|---|
| iam | `auth` | Register validasyonu ve rol varsayılanı, kullanıcı adı normalizasyonu, hatalı şifre, refresh rotasyonu, tekrar kullanımda oturumun tamamen iptali, süresi dolmuş token, logout |
| iam | `pkg/jwtauth` | İmzala/doğrula; süresi dolmuş, yanlış audience/issuer, bilinmeyen key, **HS256 algoritma karışıklığı** saldırısı; JWK dönüşümü; middleware 401/403 |
| iam | `http/router` | Register → login → me → refresh → logout akışı uçtan uca; hata kodları; JWKS'te private bilgi olmaması |
| iam | `repository` *(integration)* | Kullanıcı adı tekilliği, refresh token tüketme/tekrar kullanım/süre dolumu, aile iptali |
| sse | `pkg/jwtauth` | JWKS cache'leme, bilinmeyen `kid` için rate limitli yeniden çekme, iam-service kesintisinin 503 olarak bildirilmesi |
| sse | `http/router` | **Yetki matrisi:** her endpoint için anonim / viewer / admin durum kodları; validasyon; provider yönetimi |
| sse | `ingest`, `registry`, `service/*` | Provider'ların DB'den okunması, devre dışı provider'lar, tek provider sync'i, format adapter'ları, provider validasyonu, cache davranışı |
| sse | `repository` *(integration)* | Upsert, FTS/relevance, provider filtresi, foreign key, provider CRUD, migration seed'i |
| gateway | `http/router` | Her path'in doğru servise gitmesi; path/query/body/Authorization'ın iletilmesi; istemcinin gönderdiği `X-Forwarded-For`'un ezilmesi; request-id'nin taşınması; tanımsız path'lerin iletilmemesi; upstream kapalıyken 502; auth endpoint'lerindeki sıkı rate limit |

Integration testleri her çalıştırmada geçici bir veritabanı oluşturup sonunda siler. CI (GitHub Actions) her modül için vet, golangci-lint ve tüm testleri Postgres/Redis servisleriyle çalıştırır.

---

## Konfigürasyon

Her servis environment variable ile yapılandırılır. Compose'un okuduğu değişkenler için [`.env.example`](.env.example) dosyasına bakın.

**sse-gateway**

| Değişken | Varsayılan | Açıklama |
|---|---|---|
| `IAM_URL`, `SSE_URL` | *(zorunlu)* | Upstream servis adresleri |
| `UPSTREAM_TIMEOUT` | `30s` | Upstream istek zaman aşımı |
| `REDIS_URL` | boş | Paylaşımlı rate limit sayaçları |
| `RATE_LIMIT_MAX` / `RATE_LIMIT_WINDOW` | `100` / `1m` | `/api/*` için IP başına limit |
| `AUTH_RATE_LIMIT_MAX` / `AUTH_RATE_LIMIT_WINDOW` | `10` / `1m` | login, register ve refresh için IP başına limit |

**iam-service**

| Değişken | Varsayılan | Açıklama |
|---|---|---|
| `DATABASE_URL` | *(zorunlu)* | |
| `JWT_PRIVATE_KEY` / `JWT_PRIVATE_KEY_FILE` | boş | PEM formatında RSA anahtarı. Boşsa geçici bir anahtar üretilir |
| `JWT_ISSUER` / `JWT_AUDIENCE` | `iam-service` / `sse` | |
| `ACCESS_TOKEN_TTL` / `REFRESH_TOKEN_TTL` | `15m` / `168h` | |
| `BCRYPT_COST` | `12` | |

**sse-service**

| Değişken | Varsayılan | Açıklama |
|---|---|---|
| `DATABASE_URL` | *(zorunlu)* | |
| `JWKS_URL` | *(zorunlu)* | iam-service'in public key adresi |
| `JWT_ISSUER` / `JWT_AUDIENCE` | `iam-service` / `sse` | iam-service ile aynı olmalı |
| `REDIS_URL` / `CACHE_TTL` | boş / `90s` | |
| `SYNC_INTERVAL` | `10m` | Periyodik senkronizasyon aralığı |
| `PROVIDER_RATE_PER_SEC` / `PROVIDER_BURST` | `2` / `1` | Provider başına istek limiti |
| `PROVIDER_MAX_RETRIES` / `PROVIDER_TIMEOUT` | `3` / `5s` | |
| `SCORE_VIDEO_COEFFICIENT` / `SCORE_TEXT_COEFFICIENT` | `1.5` / `1.0` | |

---

## Bilinen Sınırlamalar ve Geliştirme Fikirleri

- **Serbest rol seçimi:** Yukarıda anlatıldığı gibi demo amaçlı. Gerçek bir sistemde admin yetkisi ayrı bir kanaldan verilmeli.
- **Refresh token'ın saklandığı yer:** Dashboard'da `HttpOnly` cookie'ye taşınabilir.
- **Kullanıcı yönetimi:** Rol değiştirme, kullanıcı silme ve şifre sıfırlama endpoint'leri yok. Süresi dolmuş refresh token'lar için periyodik bir temizlik job'ı eklenebilir.
- **Provider silme:** İçerikler provider'a foreign key ile bağlı olduğu için silme yerine devre dışı bırakma sunuluyor.
- **Provider sayfalama:** Mock yanıtlar statik olduğu için tek sayfa çekiliyor.
- **Ölçeklenme:**
  - Birden fazla sse-service instance'ında periyodik sync'in tek kopyada çalışması için Postgres advisory lock kullanılabilir.
  - Offset pagination yerine keyset pagination kullanılabilir.
  - Arama yükü büyürse Elasticsearch/OpenSearch read model olarak eklenebilir.
- **Gözlemlenebilirlik:** Prometheus metrikleri ve OpenTelemetry tracing eklenebilir. Request-id altyapısı tracing'e geçiş için hazır.
