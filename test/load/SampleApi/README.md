# SampleApi - Test Endpoints for APM Tracing

This API provides various endpoints to test different tracing scenarios for the Lang-Ango APM system.

## Basic Endpoints

```
GET /                           - Service info
GET /api/data                   - Normal data with random delay (5% slow requests)
```

## Database Simulation Endpoints

```
GET /api/db/query               - Simulate SELECT query (20-80ms)
GET /api/db/users/{id}          - Get user by ID (10-50ms)
POST /api/db/users              - Create user (30-100ms)
GET /api/db/slow-query          - Slow query with JOIN (1.5s)
GET /api/db/connection-pool     - Connection pool status (5-15ms)
```

## Business Logic Endpoints

```
POST /api/business/validate-user     - Validate user data
    Body: {"name": "string", "email": "string"}
    
POST /api/business/calculate-order  - Calculate order total
    Body: {"orderId": "string", "items": ["item1"], "discount": 0}
    
GET /api/business/inventory/check/{productId}  - Check inventory (10-40ms)

POST /api/business/payment/process  - Process payment (100-500ms)
    Body: {"amount": 100.00, "currency": "USD"}
    Note: Amount > 10000 throws exception
```

## Exception Scenarios

```
GET /api/error/throw                - InvalidOperationException
GET /api/error/argument-null        - ArgumentNullException
GET /api/error/unauthorized         - UnauthorizedAccessException
GET /api/error/not-found            - KeyNotFoundException
GET /api/error/database             - Database connection exception
GET /api/error/timeout              - 35 second timeout
GET /api/error/divide-by-zero       - DivideByZeroException
GET /api/error/null-reference       - NullReferenceException
GET /api/error/format               - FormatException
GET /api/error/conditional/{bool}   - Optional exception with inner exception
```

## Async Scenarios

```
GET /api/async/chained      - Chained async operations (validate -> process -> save)
GET /api/async/parallel     - Parallel async operations (5 concurrent tasks)
GET /api/recursive/{depth}  - Recursive async with configurable depth
```

## Cache Endpoints

```
GET /api/cache/{key}    - Get/set cached value (5 minute TTL)
```

## Testing Commands

```bash
# Database scenarios
curl -s http://localhost:8002/api/db/query
curl -s http://localhost:8002/api/db/slow-query
curl -s http://localhost:8002/api/db/users/123

# Business scenarios
curl -s -X POST http://localhost:8002/api/business/validate-user \
  -H "Content-Type: application/json" \
  -d '{"name": "John", "email": "john@example.com"}'

curl -s -X POST http://localhost:8002/api/business/payment/process \
  -H "Content-Type: application/json" \
  -d '{"amount": 99.99, "currency": "USD"}'

# Exception scenarios
curl -s http://localhost:8002/api/error/throw
curl -s http://localhost:8002/api/error/divide-by-zero
curl -s http://localhost:8002/api/error/database

# Async scenarios
curl -s http://localhost:8002/api/async/chained
curl -s http://localhost:8002/api/async/parallel
curl -s http://localhost:8002/api/async/recursive/5
```

## Jaeger UI

View traces at: http://localhost:16686

API Endpoints:
- Services: http://localhost:16686/api/services
- Traces: http://localhost:16686/api/traces?service=lang-ango&limit=10