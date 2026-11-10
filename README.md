# Content Search Service

Farklı formatlardaki (JSON, XML) içerik sağlayıcılardan veri toplayan, bu verileri standart bir formata dönüştürüp ortak bir formülle puanlayan bir arama sistemi. Anahtar kelime araması, tür ve provider filtresi, popülerlik/alakalılık sıralaması ve sayfalama sunar. Kullanıcılar kayıt olup giriş yapar. Yetki düzeyine göre viewer içerikleri görüntüleyip filtreler; admin ise ek olarak provider ekler ve senkronize eder.

Sistem üç mikroservisten oluşur:

| Servis | Sorumluluk |
|---|---|
| **sse-gateway** | Tek giriş noktası. API isteklerini ilgili servise yönlendirir. Middleware katmanında token doğrulama, rol kontrolü, kademeli rate limit, circuit breaker ve retry; ayrıca dashboard ve API dokümanı |
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
| http://localhost:3000 | Grafana: hazır dashboard'lar (giriş gerektirmez; düzenlemek için `admin` / `admin`) |
| http://localhost:9090 | Prometheus |

Dışarıya API olarak yalnızca gateway açılır. iam-service ve sse-service sadece compose ağı içinden erişilebilir; metrik portları (`:9100`) da dışarı açılmaz, onları yalnızca Prometheus okur.

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
  Browser / API ───────►│ request-id · log · IP limiti · JWT doğrulama · kullanıcı limiti · rol kontrolü     │
                        │ upstream başına circuit breaker + retry · dashboard · /docs · /openapi.yaml        │
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

- **Gateway iş mantığı içermez.** Route tablosuna göre yönlendirir ve edge görevlerini bir middleware katmanında toplar: token doğrulama, kaba rol kontrolü, rate limit, circuit breaker ve retry; ayrıca `X-Forwarded-*` header'ları, request-id ve statik dosyalar. Tanımsız API path'leri hiçbir servise iletilmez. Ayrıntılar: [Gateway Middleware Katmanı](#gateway-middleware-katmanı).
- **Token iki kez doğrulanır (defense in depth).** iam-service access token'ları private key ile imzalar ve public key'leri `/.well-known/jwks.json` adresinde yayınlar. Gateway geçersiz token'lı ve yetkisiz istekleri servise ulaşmadan reddeder. sse-service ise aynı key'lerle her isteği yeniden doğrular ve rol kontrolünü kendisi de yapar. Böylece gateway'i atlayan ya da iç ağdan gelen bir istek de yetkisiz işlem yapamaz. Maliyeti küçüktür: key'ler bellekte cache'lidir, doğrulama ağ çağrısı gerektirmez.
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
│   │   ├── proxy/               route tablosu, upstream'e iletme (Forward/Mount, Guard arayüzü)
│   │   ├── http/middleware/     auth (JWT + rol), kademeli rate limit, resilience (circuit breaker + retry)
│   │   ├── http/router/
│   │   └── web/static/          dashboard (HTML + vanilla JS, binary'ye gömülü)
│   ├── openapi/                 tüm sistemin OpenAPI spec'i
│   └── pkg/                     httpx, metrics, ratelimit, jwtauth (sadece doğrulama + jwtauthtest)
├── iam-service/
│   ├── cmd/server/
│   ├── internal/
│   │   ├── config/  domain/
│   │   ├── auth/                register/login/refresh/logout, bcrypt, token rotasyonu (+ authtest: in-memory store)
│   │   ├── repository/postgres/ users, refresh_tokens + migration'lar
│   │   ├── telemetry/           auth olay metrikleri (auth use case'ini saran decorator)
│   │   └── http/{handler,router}/
│   └── pkg/                     httpx, metrics, jwtauth (signer + verifier + JWKS + middleware)
├── sse-service/
│   ├── cmd/server/
│   ├── internal/
│   │   ├── config/  domain/  scoring/
│   │   ├── provider/            Provider interface'i, normalizasyon, jsonprovider, xmlprovider, registry
│   │   ├── ingest/              senkronizasyon
│   │   ├── repository/postgres/ contents, providers, sync_runs + migration'lar
│   │   ├── service/{search,providers}/
│   │   ├── telemetry/           arama, sync ve provider durum metrikleri
│   │   └── http/{handler,router}/
│   ├── pkg/                     cache, httpclient (rate limit + retry + circuit breaker), httpx, metrics, jwtauth
│   └── test/testdata/           gerçek provider yanıtlarından fixture'lar
├── deploy/                      postgres init script'i, (opsiyonel) imzalama anahtarı, prometheus ve grafana (dashboard'lar)
├── docker-compose.yml · Makefile · go.work · .golangci.yml · .github/workflows/ci.yml
```

Her serviste aynı katmanlı yapı kullanılır: handler → service → repository. Servisler interface'lere bağlıdır (`ContentSearcher`, `Store`, `ProviderSource`, `Builder`, `Cache`, `Authenticator`), bu sayede her katman sahte implementasyonlarla izole test edilebilir.

---

## Gateway Middleware Katmanı

Kesişen (cross-cutting) edge görevleri `sse-gateway/internal/http/middleware` paketinde toplanır. Bir API isteği şu sırayla işlenir:

```
request-id → log → IP limiti → [public auth endpoint'leri: auth limiti]
                             → JWT doğrulama → kullanıcı limiti → rol kontrolü → [sync: sync limiti]
                             → retry → circuit breaker → upstream
```

Sıranın gerekçeleri:
- IP limiti en başta: token doğrulamadan önce kaba bir sel koruması sağlar.
- Kullanıcı limiti doğrulamadan sonra: anahtarı token'daki `sub`, yani doğrulanmış kullanıcı kimliği. Doğrulanmamış bir token'dan okunan `sub`'a güvenilseydi uydurma `sub`'larla sınırsız kota açılabilirdi.
- Rol kontrolünden önce: yetkisiz denemeler de kullanıcının kotasından düşer.
- Retry dışta, breaker içte: her deneme breaker tarafından sayılır, devre açıldığı anda retry de durur.

### Kimlik doğrulama ve yetkilendirme

| Endpoint | Gateway'deki kural |
|---|---|
| `POST /auth/login`, `/auth/register`, `/auth/refresh`, `/auth/logout` | Public (token almanın yolu bunlar) |
| `GET /auth/me`, `/contents/*`, `GET /providers`, `GET /sync/runs` | Geçerli token |
| `POST`/`PATCH`/`DELETE` `/providers/*`, `POST /sync` | Geçerli token + `admin` |

Gateway yalnızca kaba kuralları uygular; iş kuralına bağlı yetki kontrolleri servislerde kalır ve sse-service token'ı yeniden doğrular. Token yoksa veya geçersizse `401`, rol yetmiyorsa `403` döner. JWKS'e ulaşılamıyorsa ve key cache'te de yoksa `503 auth_unavailable` döner.

### Kademeli rate limit

| Kademe | Anahtar | Varsayılan | Kapsam |
|---|---|---|---|
| IP | İstemci IP'si | 300 / dk | Tüm `/api` istekleri, doğrulamadan önce |
| Auth | İstemci IP'si | 10 / dk | login, register, refresh (brute-force'a karşı; henüz token yok) |
| Kullanıcı | Token'daki `sub` | 100 / dk | Token'lı tüm istekler |
| Sync | Token'daki `sub` | 5 / dk | `POST /sync` ve `POST /providers/{id}/sync` (her sync tüm provider'ları çeker) |

- **Anahtar token değil kullanıcı:** Token anahtar olsaydı kullanıcı refresh ile yeni token alıp sayacını sıfırlayabilirdi. `sub` refresh'ten sonra da aynı kalır. Aynı NAT arkasındaki kullanıcılar da tek bir IP kotasını paylaşmaz.
- **Header'lar:** Yanıtlarda `X-RateLimit-Limit`, `X-RateLimit-Remaining` ve `X-RateLimit-Reset` bulunur. Birden fazla limit uygulandığında header'lar en sıkı olanı gösterir. Limit aşılınca `429 rate_limited` ve `Retry-After` döner; dashboard kaç saniye beklenmesi gerektiğini gösterir.
- **Paylaşım:** Sayaçlar Redis'te tutulur, bu yüzden tüm gateway instance'ları aynı limiti paylaşır. Redis'e ulaşılamazsa fail-open çalışır.

### Circuit breaker

Her upstream servisin (iam-service, sse-service) ayrı bir breaker'ı vardır ([sony/gobreaker](https://github.com/sony/gobreaker)). Böylece bir servisin çökmesi diğerine giden trafiği etkilemez.

- **Hata sayılan durumlar:** Bağlantı hataları, timeout'lar ve upstream'in `502/503/504` yanıtları. `500` ve `4xx` hata sayılmaz; servis ayakta ve cevap veriyor demektir.
- **Açılma:** `BREAKER_FAILURE_THRESHOLD` (5) ardışık hatadan sonra devre açılır. Açık devrede istekler upstream'e hiç gitmez; milisaniyeler içinde `503 upstream_unavailable` ve `Retry-After` döner. Böylece hem istemci timeout kadar beklemez hem de toparlanmaya çalışan servise yük binmez.
- **Toparlanma:** `BREAKER_OPEN_TIMEOUT` (30 sn) sonra devre yarı açık duruma geçer ve bir deneme isteği geçirilir. Deneme başarılıysa devre kapanır, başarısızsa yeniden açılır.
- **İzleme:** Durum değişiklikleri loglanır, açık devreler `/readyz` çıktısında `circuit:<servis>` olarak görünür.

### Retry ve exponential backoff

Gateway'de retry kasıtlı olarak dar tutuldu, çünkü yanlış kullanıldığında zarar verir:

- **Sadece `GET` ve `HEAD` istekleri tekrar denenir.** Login, register, refresh, sync ve provider ekleme denenmez. İlk istek servise ulaşmış ama yanıt kaybolmuş olabilir. Refresh'i tekrar göndermek token'ın "tekrar kullanıldığı" sanılmasına ve oturumun kapanmasına, sync'i tekrarlamak işin iki kez yapılmasına yol açar.
- **Sadece geçici ve hızlı hatalarda denenir:** bağlantı hatası, `502` ve `503`. Timeout'lar (bağlantı kurulurken olanlar dahil) denenmez; zaman bütçesi zaten tükenmiştir.
- **En fazla `UPSTREAM_MAX_RETRIES` (2) ek deneme yapılır.** Bekleme süresi her denemede ikiye katlanır (100 ms, 200 ms, …, en fazla 1 sn). Süreye *full jitter* uygulanır, yani 0 ile bu üst sınır arasında rastgele bir süre beklenir; aynı anda hata alan istemciler toparlanan servise hep birlikte yüklenmez.
- **Devre açıksa hiç denenmez.**

Provider'lara giden istekler gateway'den geçmez. Onların rate limit, retry ve backoff'u sse-service'teki `pkg/httpclient` içinde ayrıca uygulanır (bkz. [Provider Entegrasyonu](#provider-entegrasyonu)).

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
| Biçim | JWT, **RS256** imzalı. Claim'ler: `sub` (user id), `username`, `role`, `sid` (oturum), `iss`, `aud`, `exp`, `jti` | 256 bit rastgele, opaque |
| Ömür | 15 dakika (`ACCESS_TOKEN_TTL`) | 7 gün (`REFRESH_TOKEN_TTL`) |
| Saklama | Saklanmaz, imza ile doğrulanır; iptal edilen oturumlar Redis'te kısa süreli bir listede | DB'de sadece **SHA-256 hash**'i tutulur |
| Doğrulayan | Her servis, JWKS'ten aldığı public key ile | Sadece iam-service |

- **Neden RS256 ve JWKS:** Simetrik bir imzada (HS256) token'ı doğrulayan her servis aynı zamanda token üretebilirdi. Asimetrik imzada private key yalnızca iam-service'te durur; diğer servisler sadece doğrulayabilir. `kid` header'ı sayesinde key rotasyonu yapılabilir: bilinmeyen bir `kid` görüldüğünde JWKS yeniden çekilir. Bu yeniden çekme rate limitlidir, böylece rastgele `kid`'li token'larla iam-service'e yük bindirilemez. iam-service erişilemezse sse-service bunu "geçersiz token" değil `503 auth_unavailable` olarak bildirir.
- **Refresh token rotasyonu ve tekrar kullanım tespiti:** Her refresh işleminde eski token tek bir koşullu `UPDATE` ile iptal edilir; böylece aynı token'la eşzamanlı yapılan iki refresh'ten yalnızca biri başarılı olur. Daha önce kullanılmış bir token tekrar gelirse, token'ın çalındığı varsayılır ve o oturumdaki **tüm** token'lar iptal edilir.
- **Access token iptali:** Access token'lar saklanmadığı için normalde süreleri dolana kadar (15 dk) geçerlidir. Bu açığı kapatmak için her token, ait olduğu oturumun kimliğini (`sid`, refresh token ailesinin kimliği) taşır.
  - **Ne zaman iptal edilir:** Logout'ta ve çalınmış refresh token tespit edildiğinde iam-service oturumu Redis'teki `auth:revoked-session:<sid>` listesine yazar.
  - **Kim kontrol eder:** Gateway, sse-service ve iam-service token doğrularken bu listeye bakar; o oturumun bütün access token'ları anında reddedilir (`401 invalid_token`). Dashboard bu durumda refresh dener, refresh de iptal edildiği için oturum kapanır.
  - **Neden tek tek token değil oturum:** Dashboard logout'ta sadece refresh token gönderiyor. Refresh token'dan oturum bulunuyor ve tek bir kayıtla o oturumdaki bütün access token'lar (refresh ile yenilenmiş olanlar dahil) geçersiz oluyor. Çalınmış refresh token senaryosunda saldırganın elindeki access token da böylece iptal edilir.
  - **Liste küçük kalır:** Kayıt, access token ömrü + 1 dakika (doğrulayıcıların saat toleransı) sonra kendiliğinden silinir; o noktada oturumun bütün token'larının süresi zaten dolmuştur.
  - **Redis erişilemezse:** Kontrol "fail-open" çalışır, yani token kabul edilir ve durum loglanır. Aksi halde bir Redis kesintisi bütün sistemi kilitlerdi; risk access token ömrüyle sınırlı kalır. Redis yoksa (`REDIS_URL` boş) iptal devre dışıdır ve refresh token'lar yine DB'de iptal edilir.
  - **Maliyet:** Kimliği doğrulanmış her istek gateway'de ve sse-service'te birer Redis `EXISTS` çağrısı yapar.
- **Şifreler:** bcrypt ile saklanır (cost 12, `BCRYPT_COST`). Şifre uzunluğu 8–72 byte arasında olmalı; bcrypt 72 byte'tan sonrasını yok saydığı için üst sınır bu. Var olmayan bir kullanıcı adıyla login denendiğinde de sahte bir hash ile karşılaştırma yapılır. Böylece yanıt süresinden hangi kullanıcı adlarının kayıtlı olduğu anlaşılamaz.
- **Kullanıcı adları:** Küçük harfe çevrilerek saklanır ve büyük/küçük harf duyarsız olarak tekildir.

### Roller

| İşlem | viewer | admin |
|---|:-:|:-:|
| İçerik arama, filtreleme, detay (`GET /contents`, `/contents/{id}`) | ✓ | ✓ |
| Provider listesi, son sync durumları (`GET /providers`, `/sync/runs`) | ✓ | ✓ |
| Provider ekleme / düzenleme / silme (`POST /providers`, `PATCH`/`DELETE /providers/{id}`) | | ✓ |
| Sync tetikleme (`POST /sync`, `POST /providers/{id}/sync`) | | ✓ |

**Rol tipi:** Go'da string tabanlı bir enum (`type Role string`, sabitler `RoleAdmin`/`RoleViewer`), veritabanında `TEXT` ve `CHECK (role IN ('admin','viewer'))` olarak tutuluyor.
- Integer'a göre JWT'de, JSON'da ve DB'de okunabilir, numaralandırma derdi yok.
- PostgreSQL `ENUM` tipine göre yeni bir rol eklemek tek satırlık bir `CHECK` değişikliği; ENUM'dan değer silmek ise zor.
- Geçersiz değerler hem uygulamada (`ParseRole`) hem DB'de reddediliyor.

> ⚠️ **Rol seçimi bilinçli olarak serbest.** İstendiği gibi register isteğinde rol serbestçe seçilebiliyor; yani herkes kendini admin olarak kaydedebilir. Bu demo için kabul edilebilir, ama gerçek bir sistemde public register'ın her zaman `viewer` oluşturması, admin yetkisinin ise ayrı bir kanaldan verilmesi gerekir (davet kodu, mevcut bir admin'in onayı ya da başlangıçta oluşturulan bir admin hesabı).

### Dashboard

Dashboard, framework ve build adımı olmadan yazılmış küçük bir arayüz (HTML, CSS, vanilla JS). Gateway binary'sine gömülü; açık ve koyu temayı sistem ayarına göre seçiyor ve telefon genişliğinde de çalışıyor.

- **Sonuç listesi:** Her içerik bir satır kartında gösteriliyor: sıra, tür ikonu, başlık, provider, tarih ve etiketler. Sağda skor ve aynı sayfadaki en yüksek skora göre bir çubuk var. Alakalılık sıralamasında alakalılık puanı da görünüyor.
- **Metrikler tek bakışta:** Her kartta dört metrik sütunu var. Sütunlar her satırda aynı yerde durduğu için değerler alt alta karşılaştırılabiliyor:

  | | 1 | 2 | 3 | 4 |
  |---|---|---|---|---|
  | Video | Görüntülenme | Beğeni | Süre | Beğeni oranı (beğeni / görüntülenme) |
  | Metin | Okuma süresi | Reaksiyon | Yorum | Reaksiyon / dk |

  Dördüncü sütun, skor formülündeki etkileşim oranı. Sayılar 1 milyona kadar tam yazılıyor; Türkçe kısaltmadaki "B" (bin) "milyar" gibi okunabildiği için kısaltma sadece milyonun üstünde kullanılıyor.
- **Filtreler:** Arama kutusu, tür için Tümü/Video/Metin seçimi, provider, yayın tarihi aralığı, sıralama ve sayfa boyutu. Sayfalama numaralı.
  - Kartlardaki ve detay penceresindeki etiketlere tıklanınca liste o etikete göre süzülür. Etiket filtresi sonuç sayısının yanında kaldırılabilir bir rozet olarak görünür; aynı etikete tekrar tıklamak da filtreyi kaldırır.
  - Tarih alanları birbirini sınırlar, böylece ters bir aralık seçilemez.
- **Detay penceresi:** Metriklerin tam değerleri ve skorun nasıl hesaplandığı gösteriliyor. Üç bileşen renkli bir çubukta, her biri ayrıca kendi kartında adım adım hesaplanıyor. Örnek, "Clean Architecture in Go":
  - **Temel puan × tür katsayısı:** okuma süresi 8, reaksiyon 450 ÷ 50 = 9; temel puan 8 + 9 = 17; katsayı 17 × 1 = 17.
  - **Güncellik:** kademeler (≤ 7 gün +5, ≤ 30 gün +3, ≤ 90 gün +1, daha eski 0); içeriğin düştüğü kademe vurgulanıyor ve yaşı (gün) yazıyor.
  - **Etkileşim:** reaksiyon ÷ okuma süresi = 450 ÷ 8 = 56,25; çarpanla 56,25 × 5 = 281,25.
  - **Toplam:** 17 + 0 + 281,25 = 298,25.

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
- **Tek giriş noktası:** Gateway sayesinde istemci tek bir origin görüyor (CORS gerekmiyor). Token doğrulama, rate limit ve circuit breaker gibi edge görevleri de tek yerde toplanıyor.

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

Provider'lar sse-service'in `providers` tablosunda tutulur (`name`, `url`, `format`, `enabled`). Case'teki iki provider migration ile seed edilir. Admin, dashboard'dan ya da API'den şunları yapabilir: provider ekleme, ad/URL/format düzenleme, etkinleştirme/devre dışı bırakma, silme, tek tek ya da toplu sync. Bunlar için kod değişikliği ya da restart gerekmez: her sync, etkin provider'ları DB'den okur.

**Format algılama:** Format verilmezse (ya da `auto` seçilirse) URL'den veri çekilir. Önce içeriğin ilk karakterine bakılır (`{`/`[` JSON, `<` XML); bu tahmin edilen formatın önce denenmesini sağlar. Ardından format, ilgili adapter en az bir geçerli içerik okuyabildiği takdirde kabul edilir. Yani "JSON'a benziyor" yetmez, şemaya da uymalıdır. Hiçbir adapter okuyamıyorsa (ör. bir HTML sayfası ya da farklı şemada bir JSON) istek `400` ile reddedilir. Düzenlemede URL değişir ve format açıkça verilmezse format yeniden algılanır.

**Ad değiştirme ve silme:** İçerikler provider'a adıyla bağlı (`ON UPDATE CASCADE ON DELETE CASCADE`). Ad değişince içerikler ve sync geçmişi tek bir transaction'da yeni ada taşınır. Provider silindiğinde ona ait içerikler ve sync geçmişi de silinir; dashboard bu yüzden silmeden önce onay ister. Her iki işlem de arama cache'ini anında geçersiz kılar.

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

**Sayfalama:**
- Provider'lar sayfa sayfa çekilir. İlk istek admin'in girdiği URL'ye gider; sonraki sayfalar için URL'ye `page=2`, `page=3`… eklenir, URL'deki diğer parametreler korunur.
- Her sayfa isteği provider'ın rate limit'inden, retry'ından ve circuit breaker'ından geçer.
- Bir sayfa bile başarısız olursa o provider'ın sync'i başarısız sayılır ve hiçbir şey yazılmaz. Böylece veritabanında yarım bir besleme olmaz.
- **Ne zaman durulur:**
  - Provider bir toplam bildiriyorsa (`pagination.total`, `meta.total_count`) o kadar kayıt alınınca akış tamamdır.
  - Toplam bildirmiyorsa kısa ya da boş bir sayfa son sayfadır.
  - Toplama ulaşılmadan boş sayfa gelirse ya da `PROVIDER_MAX_PAGES` (50) sınırına varılırsa, alınanlar yazılır ama akış "eksik" sayılır.
- **Sayfa parametresini yok sayan provider'lar:** Statik dosyalar, ör. case'teki mock'lar, `page=2` isteğine yine ilk sayfayla cevap verir. Bu, dönen sayfa numarasından ya da tekrar eden kayıtlardan anlaşılır. Fetch o noktada durur: aynı kayıtlar mükerrer yazılmaz ve akış eksik sayılır (mock 150 kayıt bildiriyor ama 4 tane veriyor).
- Fetch, kayıtların yanında akışın tam olup olmadığını da döndürür.

**İstek limiti yönetimi:**
- Her provider'ın kendi HTTP client'ı ve token bucket'ı var. Client provider başına cache'lenir, URL değişince yenisi oluşturulur.
- Ağ hataları, `429` ve `5xx` yanıtları exponential backoff ile yeniden denenir; `429`'da `Retry-After` header'ına uyulur.
- Yanıt boyutu 10 MB ile sınırlı.

**Circuit breaker (provider başına):**
- Sürekli hata veren bir provider'a her sync'te tekrar tekrar istek atmak hem zaman kaybı hem de o provider'a gereksiz yük. Bu yüzden her provider'ın HTTP client'ında bir breaker var.
- `PROVIDER_BREAKER_FAILURE_THRESHOLD` (5) ardışık başarısız istekten sonra devre açılır. Retry denemeleri de tek tek sayılır; devre açıldığı anda retry'lar da durur.
- Açık devrede sync o provider'ı ağ isteği yapmadan atlar ve `sync_runs`'a "circuit open" hatasıyla kaydeder; diğer provider'lar etkilenmez.
- `PROVIDER_BREAKER_OPEN_TIMEOUT` (15 dk) dolunca tek bir deneme isteği yapılır; başarılıysa devre kapanır. Süre `SYNC_INTERVAL`'dan (10 dk) uzun seçildi ki en az bir periyodik sync provider'ı atlasın.
- Breaker provider ve URL başınadır: admin bozuk URL'yi düzelttiğinde yeni URL kapalı bir devreyle başlar. Format algılama breaker kullanmaz. İptal edilen bir sync hata sayılmaz.
- Breaker durumu bellekte tutulur; servis yeniden başlarsa devreler kapalı başlar. Tek instance için yeterli.

**Kaynakta silinen içerik:**
- Provider bir kaydı artık döndürmüyorsa, kayıt bizde de aramadan ve detaydan kalkar. Kayıt silinmez, `removed_at` ile işaretlenir; provider onu tekrar döndürürse upsert işareti kaldırır ve kayıt kendiliğinden geri gelir.
- **Yalnızca güvenilir bir akıştan sonra yapılır.** Üç koşulun hepsi sağlanmalı:
  - akış tam olmalı: bütün sayfalar alınmış olmalı (bkz. sayfalama);
  - hiçbir kayıt okunamadığı için atlanmamış olmalı (atlanan kayıt kaynakta hâlâ var, sadece okunamadı);
  - akış boş olmamalı (boş bir akış genelde provider'ın bozulduğunu, her şeyi sildiğini değil, gösterir).
  Bu koşullardan biri sağlanmazsa hiçbir şey kaldırılmaz ve durum loglanır. Case'in mock'ları 150 kayıt bildirip 4 tane verdiği için eksik sayılır; onlardan hiçbir şey kaldırılmaz.
- Kaldırılan kayıt sayısı her çalışmada `sync_runs.removed` alanına ve `sync_items_removed_total` metriğine yazılır; dashboard'daki provider tablosu da "· 2 kaldırıldı" şeklinde gösterir.
- Kaldırılan kayıtlar arama sonuçlarında, detayda, içerik sayılarında ve skor yeniden hesaplamasında yer almaz.

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
- Kırılımın `explanation` alanı her bileşenin nasıl hesaplandığını adım adım verir:
  - temel puanı oluşturan terimler (metrik ÷ sabit = sonuç),
  - etkileşim oranının pay, payda ve çarpanı,
  - içeriğin yaşı, güncellik kademeleri ve hangisine düştüğü.
- Formül tek bir kural tablosunda tanımlı. Skoru hesaplayan kod açıklamayı da aynı adımlarda üretiyor; bu yüzden gösterilen hesap, gerçek hesapla hiçbir zaman ayrışamaz. Sabitler arayüzde tekrar yazılmıyor.

Örnek: provider1/v1 → `(15 + 12) × 1.5 + 0 + (1200/15000) × 10 = 41.3`

**Formülle ilgili gözlemler** (formül case'te tanımlandığı gibi, değiştirilmeden uygulandı):
- Metin içeriğin etkileşim puanı diğer bileşenlerden çok daha büyük ölçekte. Örnek veride tek makale 298 puan alırken videolar 33–70 arasında kalıyor. Gerçek bir sistemde bileşenlerin normalize edilmesi düşünülmeli.
- Mock verideki tüm tarihler 2024'e ait, bu yüzden bugün itibarıyla güncellik puanı 0. Testler sabit bir saat enjekte ederek tüm eşikleri doğruluyor.

---

## Arama ve Sıralama

`GET /api/v1/contents?q=&type=video|text&provider=<ad>&tag=<etiket>&published_from=YYYY-MM-DD&published_to=YYYY-MM-DD&sort=popularity|relevance&page=1&per_page=20`

- **Anahtar kelime:** Başlık ve etiketlerde full-text eşleşme **veya** başlıkta substring eşleşmesi. `"tam ifade"` ve `-hariç` sözdizimi desteklenir. Kullanıcı girdisindeki LIKE wildcard'ları (`%`, `_`) escape edilir.
- **Filtreler:** Hepsi birbiriyle ve anahtar kelimeyle birlikte kullanılabilir.
  - `type` ve `provider`.
  - `tag`: o etiketi taşıyan içerikler. Büyük/küçük harf duyarsız; etiketler küçük harfle saklandığı için sorgu `tags @> ARRAY[...]` şeklinde GIN index'i kullanır.
  - `published_from` / `published_to`: yayın günü aralığı (`YYYY-MM-DD`, UTC). İki uç da dahil; yalnızca biri verilirse diğer taraf açıktır. Ters aralık ya da hatalı tarih `400 invalid_input` döner.
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
- **Versiyonlu invalidation:** Cache'i geçersiz kılmak için `search:version` sayacı `INCR` edilir. Eski key'ler bir daha okunmaz ve TTL dolunca silinir. Invalidation O(1) ve atomik.
- **Sadece bir şey değiştiyse:** Sync, değişmemiş kayıtları yazmaz. Upsert, satır aynıysa güncellemeyi atlar (`IS DISTINCT FROM`); skor yeniden hesaplaması da sadece skoru gerçekten değişen kayıtları yazar. Cache yalnızca bir sync bir kaydı ekleyip değiştirdiyse, kaldırdıysa ya da skorunu değiştirdiyse temizlenir.
  - Provider verisi aynı kaldıkça cache sync'ler arasında korunur; önceden her 10 dakikada bir sıfırlanıyordu.
  - Gereksiz yazma ve onun bıraktığı ölü satırlar da oluşmaz.
  - Sync geçmişindeki `upserted` sayısı artık "eklenen ya da değişen" kayıtları gösterir; dashboard "4 kayıt · değişiklik yok" ya da "4 kayıt · 1 değişiklik" şeklinde yazar.
- **Ön ısıtma (cache warming):** Cache her geçersizleştiğinde, kullanıcının hiçbir şey yazmadan gördüğü sorgular hemen yeniden hesaplanıp cache'e yazılır. Bunlar her türün (tümü, video, metin) popülerliğe göre ilk sayfası; hem dashboard'un sayfa boyutuyla (10) hem API'nin varsayılanıyla (20), toplam 6 sorgu.
  - Böylece sync'ten sonra dashboard'u açan ilk kullanıcılar da cache'ten yanıt alır. Ön ısıtma birkaç milisaniye sürer, 5 sn ile sınırlıdır ve başarısız olursa yalnızca loglanır.
  - Kullanıcı araması sayılmaz, bu yüzden `search_requests_total` metriğini etkilemez.
  - `CACHE_WARM=false` ile kapatılabilir.
- **Stampede koruması:** Aynı key için eşzamanlı cache miss'ler `singleflight` ile tek bir DB sorgusuna indirgenir.
- **Graceful degradation:**
  - Redis yoksa aramalar doğrudan DB'den karşılanır (`BYPASS`).
  - Gateway'deki rate limiter fail-open çalışır: Redis'e ulaşamazsa istekleri reddetmez.
  - JWKS key'leri gateway'de ve sse-service'te bellekte cache'lendiği için iam-service kısa süre düşse bile mevcut token'lar doğrulanmaya devam eder.
  - Bu durumlarda `/readyz` `degraded` döner ama servis hazır kalır.

---

## İzleme (Monitoring)

`docker compose up` ile birlikte Prometheus ve Grafana da ayağa kalkar. Dashboard'lar ve datasource `deploy/grafana` altından otomatik yüklenir; elle kurulum gerekmez.

### Nasıl çalışıyor

- Her servis metriklerini **ayrı bir portta** (`METRICS_ADDR`, varsayılan `:9100`) sunar. Bu port compose dışına açılmaz, böylece `/metrics` public API portundan hiçbir zaman erişilemez.
- Prometheus üç servisi 10 saniyede bir okur ve veriyi 7 gün saklar.
- Grafana giriş istemeden dashboard'ları gösterir (anonim viewer). Düzenleme için `admin` hesabı gerekir; şifre `GRAFANA_ADMIN_PASSWORD` ile değiştirilebilir.
- Dashboard'lar elle yazılmış JSON yerine `deploy/grafana/generate.py` ile üretilir; böylece paneller tutarlı kalır. Bir paneli değiştirdikten sonra `python3 deploy/grafana/generate.py` çalıştırmak yeterli; Grafana dosyaları birkaç saniye içinde yeniden yükler.

### Dashboard'lar

**Content Search · Genel Bakış**
- Özet kutular: gateway istek hızı, 5xx oranı, p95 gecikme, açık devre sayısı.
- Servis bazında istek hızı ve p95 gecikme.
- Gateway yanıt kodları: 401/403 edge'deki yetki kontrolünü, 429 rate limit'i, 502/503/504 upstream sorunlarını gösterir.
- Kademe bazında rate limit redleri (ip, auth, user, sync).
- Gateway → servis circuit breaker durumları (Kapalı / Yarı açık / Açık), retry'lar ve açık devre yüzünden hızlıca reddedilen istekler.
- Kimlik doğrulama olayları: `invalid_credentials` patlaması şifre denemesine, `token_reused` ise çalınmış bir refresh token'a işaret eder.

**Content Search · Arama ve Senkronizasyon**
- Cache isabet oranı, saklanan içerik, etkin provider ve açık provider devresi sayıları.
- Cache durumuna göre aramalar (HIT / MISS / BYPASS) ve arama gecikmesi (p50 / p95).
- Provider bazında sync sonuçları ve sync süresi.
- Provider circuit breaker durumları.
- Provider ve türe göre içerik sayısı.

### Metrikler

| Servis | Metrik | Açıklama |
|---|---|---|
| hepsi | `http_requests_total{method,route,status}`, `http_request_duration_seconds` | İstek sayısı ve gecikme. Etiket ham path değil route kalıbıdır (`/api/v1/contents/:id`), seri sayısı sınırlı kalır |
| hepsi | `go_*`, `process_*` | Go runtime ve process metrikleri |
| gateway | `gateway_rate_limited_total{tier}` | 429 ile reddedilen istekler |
| gateway | `gateway_circuit_state{upstream}` | 0 kapalı, 1 yarı açık, 2 açık |
| gateway | `gateway_upstream_retries_total`, `gateway_circuit_rejected_total` | Retry'lar ve açık devre yüzünden hızlı redler |
| iam | `auth_events_total{event,result}` | register / login / refresh / logout sonuçları |
| sse | `search_requests_total{cache}` | HIT / MISS / BYPASS |
| sse | `sync_runs_total{provider,status}`, `sync_duration_seconds`, `sync_items_upserted_total`, `sync_items_removed_total` | Sync çalışmaları (periyodik ve manuel) ve kaynakta silindiği için gizlenen kayıtlar |
| sse | `contents_stored{provider,type}`, `provider_enabled`, `provider_circuit_state` | Prometheus okuduğu anda veritabanından ve breaker'lardan hesaplanır |

Notlar:
- **Kod değişikliği gerektirmeyen tasarım:** Servis metrikleri, gözlenen bileşenleri saran decorator'lar ile toplanır (`telemetry` paketleri). Arama servisi, sync store ve auth use case'inin koduna dokunulmadı.
- **Sayaçlar sıfırdan başlar:** Bilinen etiket kombinasyonları (rate limit kademeleri, cache durumları, beklenen auth sonuçları) servis açılırken 0 değeriyle oluşturulur. İlk değeri 1 olarak ortaya çıkan bir sayacın artışını `increase()`/`rate()` göremez ve ilk olay grafikte kaybolurdu.
- **Circuit durumları:** Grafana 11'in state-timeline paneli sayısal durumlarda value mapping'leri yok sayıyor. Bu yüzden durumlar renkli stat kutularıyla gösteriliyor.

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
| gateway | `http/router` | Her path'in doğru servise gitmesi; path/query/body/Authorization'ın iletilmesi; istemcinin gönderdiği `X-Forwarded-For`'un ezilmesi; request-id'nin taşınması; tanımsız path'lerin iletilmemesi; upstream kapalıyken 502; **edge yetki matrisi** (anonim / viewer / admin, reddedilen isteğin servise hiç ulaşmaması); auth, IP, kullanıcı ve sync limitleri (limitin token'ı değil kullanıcıyı takip etmesi, aynı IP'deki kullanıcıların ayrı kotası); açık devrenin yalnızca kendi servisini etkilemesi |
| gateway | `http/middleware` | JWT doğrulama ve rol kontrolleri, okuma/yazma ayrımı, JWKS kesintisinde 503; idempotent isteklerin tekrar denenmesi, yazma isteklerinin ve timeout'ların denenmemesi, devrenin açılıp yarı açık denemeyle kapanması, açık devrenin retry'ı durdurması, `500`'ün devreyi açmaması, backoff'un üst sınırı ve jitter'ı |
| gateway | `pkg/ratelimit` | Pencere başına limit, özel anahtar, iç içe limitlerde en sıkı header'ın raporlanması |
| sse | `pkg/httpclient` | Provider circuit breaker'ının açılıp deneme isteğiyle kapanması, her retry denemesinin sayılması ve açık devrenin retry'ı durdurması, iptalin hata sayılmaması, varsayılan olarak kapalı olması |
| sse | `registry` | Breaker'ın provider ve URL başına olması, sync'ler arasında korunması, URL düzeltilince sıfırlanması, format algılamanın breaker kullanmaması |
| hepsi | `pkg/metrics`, `telemetry`, `middleware` | Route kalıbıyla etiketleme, cache durumu / sync / auth sonucu sayaçları, scrape anında okunan durumlar ve DB hatasında scrape'in bozulmaması, rate limit / retry / devre metrikleri, sayaçların sıfırdan başlaması |
| sse | `repository` *(integration)* | Provider ve türe göre içerik sayımı |

Integration testleri her çalıştırmada geçici bir veritabanı oluşturup sonunda siler. CI (GitHub Actions) her modül için vet, golangci-lint ve tüm testleri Postgres/Redis servisleriyle çalıştırır.

---

## Konfigürasyon

Her servis environment variable ile yapılandırılır. Compose'un okuduğu değişkenler için [`.env.example`](.env.example) dosyasına bakın.

Her servis ayrıca `METRICS_ADDR` (varsayılan `:9100`) ile Prometheus metriklerini sunduğu ayrı adresi alır. Compose tarafında `PROMETHEUS_PORT` (9090), `GRAFANA_PORT` (3000) ve `GRAFANA_ADMIN_PASSWORD` (`admin`) ayarlanabilir.

**sse-gateway**

| Değişken | Varsayılan | Açıklama |
|---|---|---|
| `IAM_URL`, `SSE_URL` | *(zorunlu)* | Upstream servis adresleri |
| `UPSTREAM_TIMEOUT` | `30s` | Upstream'e yapılan her denemenin zaman aşımı |
| `JWKS_URL` | *(zorunlu)* | iam-service'in public key adresi |
| `JWT_ISSUER` / `JWT_AUDIENCE` | `iam-service` / `sse` | iam-service ile aynı olmalı |
| `REDIS_URL` | boş | Paylaşımlı rate limit sayaçları |
| `IP_RATE_LIMIT_MAX` / `IP_RATE_LIMIT_WINDOW` | `300` / `1m` | Tüm `/api` istekleri için IP başına limit |
| `AUTH_RATE_LIMIT_MAX` / `AUTH_RATE_LIMIT_WINDOW` | `10` / `1m` | login, register ve refresh için IP başına limit |
| `USER_RATE_LIMIT_MAX` / `USER_RATE_LIMIT_WINDOW` | `100` / `1m` | Kullanıcı başına limit |
| `SYNC_RATE_LIMIT_MAX` / `SYNC_RATE_LIMIT_WINDOW` | `5` / `1m` | Kullanıcı başına sync tetikleme limiti |
| `BREAKER_FAILURE_THRESHOLD` | `5` | Devreyi açan ardışık hata sayısı |
| `BREAKER_OPEN_TIMEOUT` | `30s` | Devrenin açık kalma süresi |
| `BREAKER_HALF_OPEN_REQUESTS` | `1` | Yarı açık durumda izin verilen deneme isteği |
| `UPSTREAM_MAX_RETRIES` | `2` | `GET`/`HEAD` için ek deneme sayısı (`0` kapatır) |
| `UPSTREAM_BASE_BACKOFF` / `UPSTREAM_MAX_BACKOFF` | `100ms` / `1s` | Exponential backoff başlangıcı ve üst sınırı |

**iam-service**

| Değişken | Varsayılan | Açıklama |
|---|---|---|
| `DATABASE_URL` | *(zorunlu)* | |
| `JWT_PRIVATE_KEY` / `JWT_PRIVATE_KEY_FILE` | boş | PEM formatında RSA anahtarı. Boşsa geçici bir anahtar üretilir |
| `JWT_ISSUER` / `JWT_AUDIENCE` | `iam-service` / `sse` | |
| `ACCESS_TOKEN_TTL` / `REFRESH_TOKEN_TTL` | `15m` / `168h` | |
| `BCRYPT_COST` | `12` | |
| `REDIS_URL` | boş | İptal edilen oturumların listesi; gateway ve sse-service aynı listeyi okur. Boşsa logout sonrası access token'lar süreleri dolana kadar geçerli kalır |

**sse-service**

| Değişken | Varsayılan | Açıklama |
|---|---|---|
| `DATABASE_URL` | *(zorunlu)* | |
| `JWKS_URL` | *(zorunlu)* | iam-service'in public key adresi |
| `JWT_ISSUER` / `JWT_AUDIENCE` | `iam-service` / `sse` | iam-service ile aynı olmalı |
| `REDIS_URL` / `CACHE_TTL` | boş / `90s` | |
| `CACHE_WARM` | `true` | Cache geçersizleşince varsayılan aramaları önceden hesapla |
| `SYNC_INTERVAL` | `10m` | Periyodik senkronizasyon aralığı |
| `PROVIDER_RATE_PER_SEC` / `PROVIDER_BURST` | `2` / `1` | Provider başına istek limiti |
| `PROVIDER_MAX_RETRIES` / `PROVIDER_TIMEOUT` | `3` / `5s` | |
| `PROVIDER_MAX_PAGES` | `50` | Bir fetch'te takip edilen en fazla sayfa |
| `PROVIDER_BREAKER_FAILURE_THRESHOLD` | `5` | Provider devresini açan ardışık başarısız istek sayısı (`0` kapatır) |
| `PROVIDER_BREAKER_OPEN_TIMEOUT` | `15m` | Provider devresinin açık kalma süresi |
| `SCORE_VIDEO_COEFFICIENT` / `SCORE_TEXT_COEFFICIENT` | `1.5` / `1.0` | |

---

## Bilinen Sınırlamalar ve Geliştirme Fikirleri

- **Serbest rol seçimi:** Yukarıda anlatıldığı gibi demo amaçlı. Gerçek bir sistemde admin yetkisi ayrı bir kanaldan verilmeli.
- **Refresh token'ın saklandığı yer:** Dashboard'da `HttpOnly` cookie'ye taşınabilir.
- **Kullanıcı yönetimi:** Rol değiştirme, kullanıcı silme ve şifre sıfırlama endpoint'leri yok. Süresi dolmuş refresh token'lar için periyodik bir temizlik job'ı eklenebilir.
- **Ölçeklenme:**
  - Birden fazla sse-service instance'ında periyodik sync'in tek kopyada çalışması için Postgres advisory lock kullanılabilir.
  - Offset pagination yerine keyset pagination kullanılabilir.
  - Arama yükü büyürse Elasticsearch/OpenSearch read model olarak eklenebilir.
- **Gözlemlenebilirlik:** OpenTelemetry tracing eklenebilir; request-id altyapısı hazır. Prometheus'a alert kuralları (örneğin açık devre, 5xx oranı, `token_reused`) ve Alertmanager eklenebilir.
