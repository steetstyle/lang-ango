var builder = WebApplication.CreateBuilder(args);
var app = builder.Build();

// =====================================================
// BASIC ENDPOINTS
// =====================================================

app.MapGet("/", () => Results.Ok(new { service = "SampleApi", version = "1.0.0" }));

// Normal data endpoint with random delay
app.MapGet("/api/data", async (HttpContext context) =>
{
    var stopwatch = System.Diagnostics.Stopwatch.StartNew();
    
    if (Random.Shared.Next(1, 100) <= 5)
    {
        Console.WriteLine("[SAMPLE] Slow request detected (anomaly)!");
        await Task.Delay(2000);
    }
    else
    {
        await Task.Delay(Random.Shared.Next(5, 50));
    }
    
    stopwatch.Stop();
    
    return Results.Ok(new
    {
        message = "Data fetched successfully",
        processingTime = $"{stopwatch.ElapsedMilliseconds}ms",
        status = stopwatch.ElapsedMilliseconds > 1000 ? "SLOW" : "NORMAL",
        timestamp = DateTime.UtcNow
    });
});

// =====================================================
// DATABASE SIMULATION ENDPOINTS
// =====================================================

app.MapGet("/api/db/query", async () =>
{
    await Task.Delay(Random.Shared.Next(20, 80));
    return Results.Ok(new
    {
        operation = "SELECT",
        table = "users",
        rowsReturned = Random.Shared.Next(1, 100),
        executionTime = Random.Shared.Next(10, 75)
    });
});

app.MapGet("/api/db/users/{id}", async (int id) =>
{
    await Task.Delay(Random.Shared.Next(10, 50));
    return Results.Ok(new
    {
        id = id,
        name = $"User_{id}",
        email = $"user{id}@example.com",
        createdAt = DateTime.UtcNow.AddDays(-Random.Shared.Next(1, 365))
    });
});

app.MapPost("/api/db/users", async (UserRequest user) =>
{
    await Task.Delay(Random.Shared.Next(30, 100));
    return Results.Created($"/api/db/users/{Random.Shared.Next(100, 999)}", new
    {
        id = Random.Shared.Next(100, 999),
        name = user.Name,
        email = user.Email,
        createdAt = DateTime.UtcNow
    });
});

app.MapGet("/api/db/slow-query", async () =>
{
    Console.WriteLine("[DB] Slow database query simulation");
    await Task.Delay(1500);
    return Results.Ok(new
    {
        operation = "SELECT_WITH_JOIN",
        tables = new[] { "users", "orders", "products", "inventory" },
        rowsReturned = Random.Shared.Next(1000, 5000),
        executionTime = "1450ms",
        warning = "Slow query detected"
    });
});

app.MapGet("/api/db/connection-pool", async () =>
{
    await Task.Delay(Random.Shared.Next(5, 15));
    return Results.Ok(new
    {
        poolSize = 10,
        activeConnections = Random.Shared.Next(3, 8),
        waitingRequests = Random.Shared.Next(0, 3),
        status = "healthy"
    });
});

// =====================================================
// BUSINESS LOGIC ENDPOINTS
// =====================================================

app.MapPost("/api/business/validate-user", async (UserRequest request) =>
{
    var validationService = new BusinessValidationService();
    var result = await validationService.ValidateUserAsync(request);
    return Results.Ok(result);
});

app.MapPost("/api/business/calculate-order", async (OrderRequest order) =>
{
    await Task.Delay(Random.Shared.Next(50, 200));
    var calculator = new OrderCalculator();
    var total = calculator.CalculateTotal(order);
    return Results.Ok(new
    {
        orderId = order.OrderId,
        subtotal = total.Subtotal,
        tax = total.Tax,
        discount = total.Discount,
        total = total.GrandTotal,
        status = "calculated"
    });
});

app.MapGet("/api/business/inventory/check/{productId}", async (string productId) =>
{
    await Task.Delay(Random.Shared.Next(10, 40));
    return Results.Ok(new
    {
        productId = productId,
        inStock = Random.Shared.Next(0, 100) > 20,
        quantity = Random.Shared.Next(0, 1000),
        warehouse = Random.Shared.Next(1, 5)
    });
});

app.MapPost("/api/business/payment/process", async (PaymentRequest payment) =>
{
    await Task.Delay(Random.Shared.Next(100, 500));
    
    if (payment.Amount > 10000)
    {
        throw new InvalidOperationException("Amount exceeds maximum allowed for this payment method");
    }
    
    return Results.Ok(new
    {
        transactionId = Guid.NewGuid().ToString(),
        amount = payment.Amount,
        currency = payment.Currency ?? "USD",
        status = "approved",
        processedAt = DateTime.UtcNow
    });
});

// =====================================================
// EXCEPTION SCENARIOS
// =====================================================

app.MapGet("/api/error/throw", () =>
{
    throw new InvalidOperationException("This is a test exception for APM tracing");
});

app.MapGet("/api/error/argument-null", () =>
{
    throw new ArgumentNullException("userId", "User ID cannot be null");
});

app.MapGet("/api/error/unauthorized", () =>
{
    throw new UnauthorizedAccessException("User does not have permission to access this resource");
});

app.MapGet("/api/error/not-found", () =>
{
    throw new KeyNotFoundException("The requested resource was not found");
});

app.MapGet("/api/error/database", () =>
{
    throw new Exception("Database connection failed: Connection timeout after 30s");
});

app.MapGet("/api/error/timeout", async () =>
{
    await Task.Delay(35000);
    return Results.Ok("This will never be reached");
});

app.MapGet("/api/error/divide-by-zero", () =>
{
    int x = 10;
    int y = 0;
    return Results.Ok(x / y);
});

app.MapGet("/api/error/null-reference", () =>
{
    string nullString = null;
    return Results.Ok(nullString.Length);
});

app.MapGet("/api/error/format", () =>
{
    throw new FormatException("Invalid data format received");
});

app.MapGet("/api/error/conditional/{throwError:bool}", (bool throwError) =>
{
    if (throwError)
    {
        var innerException = new TimeoutException("Inner timeout occurred");
        throw new Exception("Outer exception occurred", innerException);
    }
    return Results.Ok(new { status = "success", note = "No error thrown" });
});

// =====================================================
// CACHED ENDPOINTS (for comparison)
// =====================================================

var cache = new Dictionary<string, CacheEntry>();
var cacheLock = new object();

app.MapGet("/api/cache/{key}", (string key) =>
{
    lock (cacheLock)
    {
        if (cache.TryGetValue(key, out var entry) && entry.ExpiresAt > DateTime.UtcNow)
        {
            return Results.Ok(new
            {
                key = key,
                value = entry.Value,
                cached = true,
                expiresAt = entry.ExpiresAt
            });
        }
    }
    
    var newValue = $"cached_value_{Random.Shared.Next(1000, 9999)}";
    lock (cacheLock)
    {
        cache[key] = new CacheEntry
        {
            Value = newValue,
            ExpiresAt = DateTime.UtcNow.AddMinutes(5)
        };
    }
    
    return Results.Ok(new
    {
        key = key,
        value = newValue,
        cached = false,
        expiresAt = DateTime.UtcNow.AddMinutes(5)
    });
});

// =====================================================
// ADVANCED SCENARIOS
// =====================================================

app.MapGet("/api/async/chained", async () =>
{
    await BusinessService
        .Step1_ValidateAsync()
        .ContinueWith(_ => BusinessService.Step2_ProcessAsync())
        .Unwrap()
        .ContinueWith(_ => BusinessService.Step3_SaveAsync())
        .Unwrap();
    
    return Results.Ok(new
    {
        status = "completed",
        steps = new[] { "validate", "process", "save" },
        duration = Random.Shared.Next(100, 500)
    });
});

app.MapGet("/api/async/parallel", async () =>
{
    var tasks = Enumerable.Range(0, 5)
        .Select(i => BusinessService.DummyDbQueryAsync(i));
    
    await Task.WhenAll(tasks);
    
    return Results.Ok(new
    {
        status = "parallel_completed",
        taskCount = 5,
        processing = "parallel"
    });
});

app.MapGet("/api/recursive/{depth}", async (int depth) =>
{
    if (depth > 10) depth = 10;
    
    int result = await RecursiveAsync(depth);
    
    return Results.Ok(new
    {
        depth = depth,
        result = result,
        type = "recursive"
    });
});

app.Run();

// =====================================================
// HELPER CLASSES
// =====================================================

static async Task<int> RecursiveAsync(int depth)
{
    await Task.Delay(10);
    if (depth <= 0) return 0;
    return depth + await RecursiveAsync(depth - 1);
}

record UserRequest(string Name, string Email);
record OrderRequest(string OrderId, string[] Items, decimal Discount = 0);
record PaymentRequest(decimal Amount, string Currency = "USD", string? PaymentMethod = null);

record CacheEntry
{
    public string Value { get; set; }
    public DateTime ExpiresAt { get; set; }
}

class BusinessValidationService
{
    public async Task<object> ValidateUserAsync(UserRequest request)
    {
        await Task.Delay(Random.Shared.Next(5, 30));
        
        var errors = new List<string>();
        
        if (string.IsNullOrEmpty(request.Name))
            errors.Add("Name is required");
        
        if (string.IsNullOrEmpty(request.Email))
            errors.Add("Email is required");
        else if (!request.Email.Contains('@'))
            errors.Add("Email format is invalid");
        
        return new
        {
            isValid = errors.Count == 0,
            errors = errors,
            validatedAt = DateTime.UtcNow
        };
    }
}

class OrderCalculator
{
    public OrderTotal CalculateTotal(OrderRequest order)
    {
        var subtotal = order.Items.Length * 9.99m;
        var tax = subtotal * 0.08m;
        var discount = order.Discount;
        return new OrderTotal(subtotal, tax, discount);
    }
}

class OrderTotal
{
    public decimal Subtotal { get; }
    public decimal Tax { get; }
    public decimal Discount { get; }
    public decimal GrandTotal => Subtotal + Tax - Discount;
    
    public OrderTotal(decimal subtotal, decimal tax, decimal discount)
    {
        Subtotal = subtotal;
        Tax = tax;
        Discount = discount;
    }
}

static class BusinessService
{
    public static async Task Step1_ValidateAsync()
    {
        await Task.Delay(Random.Shared.Next(20, 50));
        Console.WriteLine("[BUSINESS] Step 1: Validation completed");
    }
    
    public static async Task Step2_ProcessAsync()
    {
        await Task.Delay(Random.Shared.Next(50, 150));
        Console.WriteLine("[BUSINESS] Step 2: Processing completed");
    }
    
    public static async Task Step3_SaveAsync()
    {
        await Task.Delay(Random.Shared.Next(30, 80));
        Console.WriteLine("[BUSINESS] Step 3: Save completed");
    }
    
    public static async Task DummyDbQueryAsync(int queryId)
    {
        await Task.Delay(Random.Shared.Next(10, 50));
        Console.WriteLine($"[BUSINESS] DB Query {queryId} completed");
    }
}