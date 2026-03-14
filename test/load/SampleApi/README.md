# SampleApi - Test Endpoints for APM Tracing

This API provides various endpoints to test different tracing scenarios for the Lang-Ango APM system.

**Database**: SQLite with Entity Framework Core (real database operations)

## Quick Start

```bash
# Start SampleApi with tracing enabled
export LANGANGO_SOCKET_PATH=/tmp/langango.sock
export DOTNET_STARTUP_HOOKS=/path/to/langango/dotnet/startup-hook/LangAngo.StartupHook/publish/langango.dll
export LD_LIBRARY_PATH=/path/to/langango/dotnet/startup-hook:$LD_LIBRARY_PATH
dotnet run -c Release --urls "http://localhost:8002"
```

## Endpoints

### Basic Endpoints
```
GET /                           - Service info
GET /api/data                   - Normal data with random delay
```

### Real Database Operations (SQLite + EF Core)

#### Users
```
GET    /api/db/users            - SELECT * FROM users
GET    /api/db/users/{id}       - SELECT * FROM users WHERE id = {id}
POST   /api/db/users            - INSERT INTO users
PUT    /api/db/users/{id}       - UPDATE users
DELETE /api/db/users/{id}       - DELETE FROM users WHERE id = {id}
```

#### Products
```
GET    /api/db/products         - SELECT * FROM products
```

#### Orders (with JOIN)
```
GET    /api/db/orders           - JOIN: orders + users + products
POST   /api/db/orders           - INSERT order + UPDATE product stock (transaction)
```

#### Statistics
```
GET    /api/db/stats            - Aggregation queries (COUNT, SUM)
GET    /api/db/slow-query       - Slow query (>500ms) with complex processing
```

### Business Logic with Real DB
```
POST   /api/business/login              - Login with DB lookup
POST   /api/business/checkout          - Multi-item transaction
GET    /api/business/user-orders/{id}  - User orders with product JOIN
```

### Exception Scenarios
```
GET    /api/error/throw              - InvalidOperationException
GET    /api/error/argument-null      - ArgumentNullException
GET    /api/error/unauthorized       - UnauthorizedAccessException
GET    /api/error/not-found          - KeyNotFoundException
GET    /api/error/database-generic    - Database connection exception
GET    /api/error/divide-by-zero     - DivideByZeroException
GET    /api/error/null-reference     - NullReferenceException
GET    /api/error/format             - FormatException
```

### Async Scenarios
```
GET    /api/async/chained     - Chained async with DB queries
GET    /api/async/parallel    - Parallel async queries
```

## Example Requests

```bash
# SELECT: Get all users
curl -s http://localhost:8002/api/db/users | jq

# JOIN: Orders with users and products
curl -s http://localhost:8002/api/db/orders | jq

# INSERT: Create user
curl -s -X POST http://localhost:8002/api/db/users \
  -H "Content-Type: application/json" \
  -d '{"name": "John", "email": "john@example.com", "role": "User"}' | jq

# UPDATE: Update user
curl -s -X PUT http://localhost:8002/api/db/users/1 \
  -H "Content-Type: application/json" \
  -d '{"name": "John Updated"}' | jq

# DELETE: Delete user
curl -s -X DELETE http://localhost:8002/api/db/users/5 | jq

# Transaction: Create order (updates stock)
curl -s -X POST http://localhost:8002/api/db/orders \
  -H "Content-Type: application/json" \
  -d '{"userId": 1, "productId": 2, "quantity": 3}' | jq

# Complex: Multi-item checkout
curl -s -X POST http://localhost:8002/api/business/checkout \
  -H "Content-Type: application/json" \
  -d '{
    "userId": 1,
    "items": [
      {"productId": 1, "quantity": 1},
      {"productId": 3, "quantity": 2}
    ]
  }' | jq

# Aggregation: Database statistics
curl -s http://localhost:8002/api/db/stats | jq

# Slow query simulation
curl -s http://localhost:8002/api/db/slow-query | jq

# Exception handling
curl -s http://localhost:8002/api/error/throw
curl -s http://localhost:8002/api/error/divide-by-zero
curl -s http://localhost:8002/api/error/null-reference
```

## Tracing Verification

```bash
# Check Jaeger UI
open http://localhost:16686

# API to get traces
curl -s "http://localhost:16686/api/services" | jq
curl -s "http://localhost:16686/api/traces?service=lang-ango&limit=5" | jq
```

## Database Schema

```sql
-- Users
CREATE TABLE Users (
    Id INTEGER PRIMARY KEY,
    Name TEXT NOT NULL,
    Email TEXT NOT NULL,
    Role TEXT NOT NULL,
    CreatedAt TEXT NOT NULL
);

-- Products
CREATE TABLE Products (
    Id INTEGER PRIMARY KEY,
    Name TEXT NOT NULL,
    Price TEXT NOT NULL,
    Stock INTEGER NOT NULL
);

-- Orders
CREATE TABLE Orders (
    Id INTEGER PRIMARY KEY,
    UserId INTEGER NOT NULL,
    ProductId INTEGER NOT NULL,
    Quantity INTEGER NOT NULL,
    TotalPrice TEXT NOT NULL,
    Status TEXT NOT NULL,
    OrderDate TEXT NOT NULL
);
```

## Seeded Data

Initial data is seeded on first run:
- 3 users (Alice, Bob, Charlie)
- 4 products (Laptop, Mouse, Keyboard, Monitor)
- 2 orders