# Lang-Ango E2E Load Test

## Test Senaryosu

Bu test, Lang-Ango'nun gerçek bir ASP.NET Core uygulamasında:
- Adaptive Sampling'in çalışmasını
- Ring Buffer'ın yüksek load altında dayanıklılığını
- Anomali tespitini

test eder.

## Kurulum

```bash
# 1. Jaeger'ı başlat
docker-compose up -d jaeger

# 2. Setup scriptini çalıştır
chmod +x test/load/setup.sh
cd test/load
./setup.sh
```

## Testi Çalıştır

```bash
# k6 yüklü değilse:
# brew install k6  (macOS)
# apt install k6   (Linux)

k6 run load-test.js
```

## Beklenen Sonuçlar

### Normal Mod (Başlangıç)
- CPU kullanımı < %1
- Sadece temel metrikler akar

### Anomali Tespiti
```
[SMART] 🚨 Anomali tespit edildi: /api/data
[SMART]   Süre: 2000ms, Ortalama: 45ms
[SMART]   → Derin analiz başlatılyor
```

### Jaeger
- Normal istekler: kısa spanler
- Yavaş istekler: Child spanler (fn:Task.Delay, fn:SampleApi.Handler)

## Dosyalar

- `SampleApi/` - Test ASP.NET Core uygulaması
- `setup.sh` - Sistem başlatma scripti
- `load-test.js` - k6 yük testi
