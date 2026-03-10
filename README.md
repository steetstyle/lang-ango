# Lang-Ango

eBPF tabanlı, kod değişikliği gerektirmeyen (zero‑code) OpenTelemetry otomatik enstrümantasyon ajanı

## Genel Bakış

Lang-Ango, dağıtık uygulamalar için **uygulama koduna dokunmadan** gözlemlenebilirlik sağlayan bir ajandır.  
Linux çekirdeği üzerinde çalışan eBPF programları ile ağ trafiğini ve veritabanı çağrılarını yakalar, kullanıcı alanındaki Go ajanı ile bu verileri **OpenTelemetry uyumlu metrik ve izlere (trace)** dönüştürür.

Hedef:

- Kod içerisine ekstra SDK/agent eklemeden,
- Farklı dillerde yazılmış servisleri (Go, .NET, Java vb.) ortak bir modelle gözlemleyebilmek,
- Toplanan veriyi tamamen **açık standartlarla** (OTLP, Prometheus) dış sistemlere (Grafana, Tempo, Jaeger vb.) iletmektir.

## Özellikler

- **Kod Değişikliği Gerektirmez (Zero‑Code)**: Uygulama koduna agent veya SDK eklemeden çalışır.
- **Çoklu Protokol Desteği**: HTTP, gRPC, PostgreSQL, MySQL, Redis, Kafka vb.
- **TLS Trafik Analizi**: Şifreli TLS bağlantıları için kullanıcı alanı kancaları (hook) ile birlikte çalışabilir.
- **Dağıtık İzleme (Tracing)**: W3C Trace Context (`traceparent`) başlığı ile bağlam yayılımı.
- **Kubernetes Entegrasyonu**: Pod / Service bilgisini trace ve metriklere etiket olarak ekler.
- **OpenTelemetry Desteği**: OTLP çıkışı ve Prometheus uyumlu metrikler.
- **Dil Bağımsız Çekirdek**: Çekirdek katmanı tamamen language‑agnostic; uygulama tarafında ek kütüphane gerektirmez.

## Mimari

Lang-Ango'nun tasarımı, iki ana katmanı birleştiren hibrit bir yaklaşıma dayanır:

- **Kernel Katmanı (eBPF)**: Ağ trafiğini, L7 protokollerini (HTTP, PostgreSQL, TDS/MSSQL vb.), TLS oturumlarını ve soket düzeyindeki ilişkileri yakalar. Bu katman tamamen dilden bağımsızdır.
- **Kullanıcı Alanı Katmanı (User-Space Agent + Language Hooks)**:
  - Go ile yazılmış kullanıcı alanı ajanı, eBPF event'lerini okuyup OpenTelemetry span'lerine dönüştürür, Kubernetes metadata'sı ile zenginleştirir ve OTLP üzerinden dışa aktarır.
  - İsteğe bağlı dil-spesifik hook'lar (ör. .NET profiler) ile sadece ağ trafiğine yansımayan iç metod çağrıları, özel exception tipleri ve detaylı stack trace bilgileri de toplanabilir.

Bu mimarinin temel motivasyonu:

- Uygulama koduna dokunmadan **servis sınırlarının dışından** mümkün olan en zengin bağlamı yakalamak,
- Ağ ve veritabanı seviyesindeki sinyallerle, uygulama içi davranışı (ör. kritik metodlar, domain spesifik exception'lar) tek bir bütünsel trace modeli içinde birleştirmek,
- Toplanan veriyi tamamen açık standartlarla (OTLP, Prometheus) dış dünyaya sunmaktır.

Yüksek seviye bileşen görünümü:

```
┌─────────────────────────────────────────────┐
│           Kubernetes / Host                  │
│  ┌─────────────────────────────────────┐   │
│  │         Lang-Ango Agent            │   │
│  │  ┌─────────┐  ┌─────────┐          │   │
│  │  │ eBPF    │  │  User   │          │   │
│  │  │ Kernel  │◄─┤  Space  │◄──► OTLP │   │
│  │  │ Programs│  │  Agent  │   Export │   │
│  │  └─────────┘  └─────────┘          │   │
│  └─────────────────────────────────────┘   │
└─────────────────────────────────────────────┘
```

## Hızlı Başlangıç

### Ön Koşullar

- Linux kernel 5.8+ (BTF etkin)
- Go 1.21+
- clang/llvm
- eBPF için root/sudo yetkisi

### Derleme

```bash
# Bağımlılıkları indir
make deps

# eBPF programlarını derle
make bpf

# Ajanı derle
make build

# Hepsini bir arada
make all
```

### Çalıştırma

```bash
# Konfigürasyon dosyası ile
sudo ./bin/lang-ango -config config.yaml

# Sadece belirli bir portu dinlemek için
sudo ./bin/lang-ango -port 8080

# Belirli bir PID için
sudo ./bin/lang-ango -pid 12345
```

### Docker ile Çalıştırma

```bash
# İmajı oluştur
make docker

# Docker'da çalıştır
docker run -d \
  --privileged \
  -v /sys/kernel/debug:/sys/kernel/debug \
  -v /sys/kernel/btf:/sys/kernel/btf:ro \
  lang-ango:latest
```

### Uçtan Uca E2E Test Ortamı (İsteğe Bağlı)

Lang-Ango ile gerçek bir .NET/ASP.NET uygulaması, veritabanı ve OpenTelemetry Collector üzerinde davranışı uçtan uca doğrulamak için, proje içinde hazır bir `docker-compose` kurgusu bulunur:

```bash
docker compose -f docker-compose.e2e.yml up --build e2e-tests
```

Bu komut:

- Postgres veritabanını (`postgres` servisi),
- Örnek ASP.NET Core uygulamasını (`autoapi` – `dotnet/AutoApi.E2ETestApp`),
- OpenTelemetry Collector'ı (`otel-collector`),
- Lang-Ango ajanını (`lang-ango-agent`),
- Go ile yazılmış E2E testlerini (`e2e-tests`)

aynı ağ üzerinde ayağa kaldırır.

E2E senaryonun amacı, gerçek bir HTTP isteğinin:

- Uygulama katmanında birden fazla SQL sorgusu ve özel bir exception (ör. `SbmException`) üretmesini,
- eBPF katmanında bu SQL/HTTP/TLS sinyallerinin yakalanmasını,
- Kullanıcı alanı ajanında bunların tek bir izleme bağlamında (trace) birleştirilip OTLP üzerinden kolektöre gönderildiğini

otomatik olarak doğrulamaktır.

Bu ortam, CI/CD boru hatlarına (pipeline) entegre edilerek geriye dönük uyumluluk (regresyon), görünürlük derinliği ve ajan performans etkisinin sürekli olarak test edilmesini sağlar.

### Kubernetes

```bash
# Deploy
kubectl apply -f deployments/kubernetes/
```

## Konfigürasyon

Tüm seçenekler için `config.yaml` dosyasına bakabilirsiniz. Örnek bir minimum konfigürasyon:

```yaml
service:
  name: lang-ango
  version: "0.1.0"
  
discovery:
  ports: [80, 443, 8080]
  interval: 10s

otel:
  endpoint: "localhost:4317"
  insecure: true

prometheus:
  enabled: true
  port: 9400
```

## Metrikler

Ajan, OpenTelemetry uyumlu aşağıdaki temel metrikleri üretir:

- `http.server.duration` – HTTP istek süreleri (histogram)
- `http.server.request_count` – Toplam HTTP istek sayısı (counter)
- `http.server.error_count` – HTTP hata sayısı (counter)
- `db.client.duration` – Veritabanı çağrı süreleri (histogram)

## Desteklenen Diller

Çekirdek eBPF katmanı dil bağımsızdır; aşağıdaki diller için tipik kullanım senaryoları desteklenir:

| Dil     | HTTP | HTTPS/TLS | Veritabanı |
|---------|------|-----------|-----------|
| Go      | ✓    | ✓         | ✓         |
| Java    | ✓    | Sınırlı   | ✓         |
| Python  | ✓    | ✓         | ✓         |
| Node.js | ✓    | ✓         | ✓         |
| .NET    | ✓    | Sınırlı   | ✓         |
| Rust    | ✓    | ✓         | ✓         |

“Sınırlı” ifadesi, ilgili dilde TLS içi görünürlüğün tamamen çekirdekten sağlanamadığı, ek kullanıcı alanı hook’larına veya proxy mimarisine ihtiyaç duyulabileceği anlamına gelir.

## Lisans

Apache License 2.0
