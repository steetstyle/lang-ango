using Microsoft.EntityFrameworkCore;

var builder = WebApplication.CreateBuilder(args);

// Add SQLite database
builder.Services.AddDbContext<AppDbContext>(options =>
    options.UseSqlite("Data Source=sample.db"));

var app = builder.Build();

// Initialize database
using (var scope = app.Services.CreateScope())
{
    var db = scope.ServiceProvider.GetRequiredService<AppDbContext>();
    db.Database.EnsureCreated();
    
    // Seed initial data if empty
    if (!db.Users.Any())
    {
        db.Users.AddRange(
            new User { Name = "Alice", Email = "alice@example.com", Role = "Admin" },
            new User { Name = "Bob", Email = "bob@example.com", Role = "User" },
            new User { Name = "Charlie", Email = "charlie@example.com", Role = "User" }
        );
        db.Products.AddRange(
            new Product { Name = "Laptop", Price = 999.99m, Stock = 50 },
            new Product { Name = "Mouse", Price = 29.99m, Stock = 200 },
            new Product { Name = "Keyboard", Price = 79.99m, Stock = 150 },
            new Product { Name = "Monitor", Price = 299.99m, Stock = 75 }
        );
        db.Orders.AddRange(
            new Order { UserId = 1, ProductId = 1, Quantity = 1, TotalPrice = 999.99m, Status = "Completed" },
            new Order { UserId = 2, ProductId = 2, Quantity = 2, TotalPrice = 59.98m, Status = "Pending" }
        );
        db.SaveChanges();
        Console.WriteLine("[DB] Seeded initial data");
    }
}

// =====================================================
// BASIC ENDPOINTS
// =====================================================

app.MapGet("/", () => Results.Ok(new { service = "SampleApi", version = "2.0.0", database = "SQLite" }));

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
// REAL DATABASE ENDPOINTS (SQLite + EF Core)
// =====================================================

// GET all users
app.MapGet("/api/db/users", async (AppDbContext db) =>
{
    var users = await db.Users.ToListAsync();
    return Results.Ok(new
    {
        operation = "SELECT",
        table = "users",
        rowCount = users.Count,
        data = users,
        executedAt = DateTime.UtcNow
    });
});

// GET user by ID
app.MapGet("/api/db/users/{id}", async (int id, AppDbContext db) =>
{
    var user = await db.Users.FindAsync(id);
    if (user == null)
    {
        return Results.NotFound(new { error = "User not found", userId = id });
    }
    return Results.Ok(new
    {
        operation = "SELECT",
        table = "users",
        row = user,
        executedAt = DateTime.UtcNow
    });
});

// CREATE user
app.MapPost("/api/db/users", async (User user, AppDbContext db) =>
{
    user.CreatedAt = DateTime.UtcNow;
    db.Users.Add(user);
    await db.SaveChangesAsync();
    return Results.Created($"/api/db/users/{user.Id}", new
    {
        operation = "INSERT",
        table = "users",
        row = user,
        rowsAffected = 1,
        executedAt = DateTime.UtcNow
    });
});

// UPDATE user
app.MapPut("/api/db/users/{id}", async (int id, User updateUser, AppDbContext db) =>
{
    var user = await db.Users.FindAsync(id);
    if (user == null)
    {
        return Results.NotFound(new { error = "User not found", userId = id });
    }
    
    user.Name = updateUser.Name ?? user.Name;
    user.Email = updateUser.Email ?? user.Email;
    user.Role = updateUser.Role ?? user.Role;
    
    await db.SaveChangesAsync();
    
    return Results.Ok(new
    {
        operation = "UPDATE",
        table = "users",
        row = user,
        rowsAffected = 1,
        executedAt = DateTime.UtcNow
    });
});

// DELETE user
app.MapDelete("/api/db/users/{id}", async (int id, AppDbContext db) =>
{
    var user = await db.Users.FindAsync(id);
    if (user == null)
    {
        return Results.NotFound(new { error = "User not found", userId = id });
    }
    
    db.Users.Remove(user);
    await db.SaveChangesAsync();
    
    return Results.Ok(new
    {
        operation = "DELETE",
        table = "users",
        rowId = id,
        rowsAffected = 1,
        executedAt = DateTime.UtcNow
    });
});

// GET all products
app.MapGet("/api/db/products", async (AppDbContext db) =>
{
    var products = await db.Products.ToListAsync();
    return Results.Ok(new
    {
        operation = "SELECT",
        table = "products",
        rowCount = products.Count,
        data = products,
        executedAt = DateTime.UtcNow
    });
});

// GET all orders with JOIN (users + products)
app.MapGet("/api/db/orders", async (AppDbContext db) =>
{
    var orders = await (from o in db.Orders
                        join u in db.Users on o.UserId equals u.Id
                        join p in db.Products on o.ProductId equals p.Id
                        select new
                        {
                            o.Id,
                            UserName = u.Name,
                            ProductName = p.Name,
                            o.Quantity,
                            o.TotalPrice,
                            o.Status,
                            o.OrderDate
                        }).ToListAsync();
    
    return Results.Ok(new
    {
        operation = "SELECT_WITH_JOIN",
        tables = new[] { "orders", "users", "products" },
        rowCount = orders.Count,
        data = orders,
        executedAt = DateTime.UtcNow
    });
});

// Complex query - aggregation
app.MapGet("/api/db/stats", async (AppDbContext db) =>
{
    var userCount = await db.Users.CountAsync();
    var productCount = await db.Products.CountAsync();
    var orderCount = await db.Orders.CountAsync();
    var totalRevenue = await db.Orders
        .Where(o => o.Status == "Completed")
        .SumAsync(o => o.TotalPrice);
    
    // Intentional delay to simulate complex query
    await Task.Delay(50);
    
    return Results.Ok(new
    {
        operation = "AGGREGATION",
        queries = new[]
        {
            "SELECT COUNT(*) FROM users",
            "SELECT COUNT(*) FROM products",
            "SELECT COUNT(*) FROM orders",
            "SELECT SUM(total_price) FROM orders WHERE status='Completed'"
        },
        result = new
        {
            userCount,
            productCount,
            orderCount,
            totalRevenue
        },
        executedAt = DateTime.UtcNow
    });
});

// Slow query simulation (complex join with multiple tables)
app.MapGet("/api/db/slow-query", async (AppDbContext db) =>
{
    var stopwatch = System.Diagnostics.Stopwatch.StartNew();
    
    // Force complex query with multiple operations
    var users = await db.Users.ToListAsync();
    var products = await db.Products.ToListAsync();
    var orders = await db.Orders.ToListAsync();
    
    // Simulate expensive in-memory operations
    var complexResult = users
        .SelectMany(u => orders.Where(o => o.UserId == u.Id), (u, o) => new { User = u, Order = o })
        .Select(x => new
        {
            x.User.Name,
            x.Order.TotalPrice,
            x.Order.Status,
            Product = products.FirstOrDefault(p => p.Id == x.Order.ProductId)?.Name
        })
        .ToList();
    
    stopwatch.Stop();
    
    // If too fast, add artificial delay
    if (stopwatch.ElapsedMilliseconds < 500)
    {
        await Task.Delay(500 - (int)stopwatch.ElapsedMilliseconds);
    }
    
    return Results.Ok(new
    {
        operation = "COMPLEX_JOIN_WITH_PROCESSING",
        tables = new[] { "users", "orders", "products" },
        rowCount = complexResult.Count,
        executionTime = $"{stopwatch.ElapsedMilliseconds}ms",
        warning = "Slow query detected",
        data = complexResult,
        executedAt = DateTime.UtcNow
    });
});

// Create new order (transaction simulation)
app.MapPost("/api/db/orders", async (OrderRequest req, AppDbContext db) =>
{
    using var transaction = await db.Database.BeginTransactionAsync();
    
    try
    {
        var product = await db.Products.FindAsync(req.ProductId);
        if (product == null)
        {
            return Results.BadRequest(new { error = "Product not found", productId = req.ProductId });
        }
        
        if (product.Stock < req.Quantity)
        {
            return Results.BadRequest(new { error = "Insufficient stock", available = product.Stock, requested = req.Quantity });
        }
        
        // Create order
        var order = new Order
        {
            UserId = req.UserId,
            ProductId = req.ProductId,
            Quantity = req.Quantity,
            TotalPrice = product.Price * req.Quantity,
            Status = "Pending",
            OrderDate = DateTime.UtcNow
        };
        
        db.Orders.Add(order);
        
        // Update stock
        product.Stock -= req.Quantity;
        
        await db.SaveChangesAsync();
        await transaction.CommitAsync();
        
        return Results.Created($"/api/db/orders/{order.Id}", new
        {
            operation = "INSERT_WITH_UPDATE",
            tables = new[] { "orders", "products" },
            order = order,
            rowsAffected = 2,
            executedAt = DateTime.UtcNow
        });
    }
    catch
    {
        await transaction.RollbackAsync();
        throw;
    }
});

// =====================================================
// EXCEPTION SCENARIOS (Real DB errors)
// =====================================================

app.MapGet("/api/error/db-connection", async (AppDbContext db) =>
{
    // Try to access a table that doesn't exist
    await db.Database.ExecuteSqlRawAsync("SELECT * FROM nonexistent_table");
    return Results.Ok("This will never be reached");
});

app.MapGet("/api/error/db-constraint", async (AppDbContext db) =>
{
    // Violate unique constraint
    var user = new User { Name = "Test", Email = "duplicate@test.com", Role = "User" };
    db.Users.Add(user);
    await db.Users.AddAsync(user); // Add same entity twice
    await db.SaveChangesAsync();
    return Results.Ok("This will never be reached");
});

app.MapGet("/api/error/db-timeout", async (AppDbContext db) =>
{
    // Long running query that might timeout
    await db.Database.ExecuteSqlRawAsync("WITH RECURSIVE cnt(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM cnt LIMIT 10000000) SELECT count(*) FROM cnt");
    return Results.Ok("This might timeout");
});

// =====================================================
// BUSINESS LOGIC WITH REAL DB
// =====================================================

app.MapPost("/api/business/login", async (LoginRequest req, AppDbContext db) =>
{
    var user = await db.Users.FirstOrDefaultAsync(u => u.Email == req.Email);
    
    if (user == null)
    {
        return Results.Unauthorized();
    }
    
    // Simulate password check (in real app, use proper auth)
    await Task.Delay(Random.Shared.Next(10, 30));
    
    return Results.Ok(new
    {
        userId = user.Id,
        name = user.Name,
        role = user.Role,
        loginTime = DateTime.UtcNow
    });
});

app.MapGet("/api/business/user-orders/{userId}", async (int userId, AppDbContext db) =>
{
    var user = await db.Users.FindAsync(userId);
    if (user == null)
    {
        return Results.NotFound(new { error = "User not found", userId });
    }
    
    var orders = await db.Orders
        .Where(o => o.UserId == userId)
        .Join(db.Products,
              o => o.ProductId,
              p => p.Id,
              (o, p) => new { o.Id, Product = p.Name, o.Quantity, o.TotalPrice, o.Status, o.OrderDate })
        .ToListAsync();
    
    return Results.Ok(new
    {
        user = new { id = user.Id, name = user.Name, email = user.Email },
        orders,
        totalSpent = orders.Sum(o => o.TotalPrice)
    });
});

app.MapPost("/api/business/checkout", async (CheckoutRequest req, AppDbContext db) =>
{
    using var transaction = await db.Database.BeginTransactionAsync();
    
    try
    {
        var user = await db.Users.FindAsync(req.UserId);
        if (user == null)
        {
            return Results.BadRequest(new { error = "User not found", userId = req.UserId });
        }
        
        var results = new List<object>();
        decimal totalAmount = 0;
        
        foreach (var item in req.Items)
        {
            var product = await db.Products.FindAsync(item.ProductId);
            if (product == null)
            {
                return Results.BadRequest(new { error = "Product not found", productId = item.ProductId });
            }
            
            if (product.Stock < item.Quantity)
            {
                return Results.BadRequest(new { error = "Insufficient stock", product = product.Name, available = product.Stock, requested = item.Quantity });
            }
            
            var order = new Order
            {
                UserId = req.UserId,
                ProductId = item.ProductId,
                Quantity = item.Quantity,
                TotalPrice = product.Price * item.Quantity,
                Status = "Completed",
                OrderDate = DateTime.UtcNow
            };
            
            db.Orders.Add(order);
            product.Stock -= item.Quantity;
            totalAmount += order.TotalPrice;
            
            results.Add(new { orderId = order.Id, product = product.Name, quantity = item.Quantity, price = order.TotalPrice });
        }
        
        await db.SaveChangesAsync();
        await transaction.CommitAsync();
        
        return Results.Ok(new
        {
            message = "Checkout successful",
            userId = req.UserId,
            items = results,
            totalAmount,
            transactionTime = DateTime.UtcNow
        });
    }
    catch
    {
        await transaction.RollbackAsync();
        throw;
    }
});

// =====================================================
// REMAINING EXCEPTION SCENARIOS
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
    throw new KeyNotFoundException("The requested resource was not found in database");
});

app.MapGet("/api/error/database-generic", () =>
{
    throw new Exception("Database connection failed: Connection timeout after 30s");
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
    return Results.Ok(nullString!.Length);
});

app.MapGet("/api/error/format", () =>
{
    throw new FormatException("Invalid data format received from database");
});

// =====================================================
// ASYNC SCENARIOS
// =====================================================

app.MapGet("/api/async/chained", async (AppDbContext db) =>
{
    // Step 1: Validate user
    var userCount = await db.Users.CountAsync();
    await Task.Delay(20);
    Console.WriteLine($"[BUSINESS] Step 1: Validated {userCount} users");
    
    // Step 2: Process orders
    var orders = await db.Orders.ToListAsync();
    await Task.Delay(50);
    Console.WriteLine($"[BUSINESS] Step 2: Processed {orders.Count} orders");
    
    // Step 3: Update stats
    var revenue = await db.Orders.Where(o => o.Status == "Completed").SumAsync(o => o.TotalPrice);
    await Task.Delay(30);
    Console.WriteLine($"[BUSINESS] Step 3: Calculated revenue ${revenue}");
    
    return Results.Ok(new
    {
        status = "completed",
        steps = new[] { "validate_users", "process_orders", "calculate_revenue" },
        result = new { userCount, orderCount = orders.Count, totalRevenue = revenue }
    });
});

app.MapGet("/api/async/parallel", async (AppDbContext db) =>
{
    var userTask = db.Users.ToListAsync();
    var productTask = db.Products.ToListAsync();
    var orderTask = db.Orders.ToListAsync();
    
    await Task.WhenAll(userTask, productTask, orderTask);
    
    return Results.Ok(new
    {
        status = "parallel_completed",
        users = userTask.Result.Count,
        products = productTask.Result.Count,
        orders = orderTask.Result.Count
    });
});

app.Run();

// =====================================================
// DATABASE MODELS
// =====================================================

public class AppDbContext : DbContext
{
    public AppDbContext(DbContextOptions<AppDbContext> options) : base(options) { }
    
    public DbSet<User> Users { get; set; }
    public DbSet<Product> Products { get; set; }
    public DbSet<Order> Orders { get; set; }
}

public class User
{
    public int Id { get; set; }
    public string Name { get; set; } = string.Empty;
    public string Email { get; set; } = string.Empty;
    public string Role { get; set; } = "User";
    public DateTime CreatedAt { get; set; } = DateTime.UtcNow;
}

public class Product
{
    public int Id { get; set; }
    public string Name { get; set; } = string.Empty;
    public decimal Price { get; set; }
    public int Stock { get; set; }
}

public class Order
{
    public int Id { get; set; }
    public int UserId { get; set; }
    public int ProductId { get; set; }
    public int Quantity { get; set; }
    public decimal TotalPrice { get; set; }
    public string Status { get; set; } = "Pending";
    public DateTime OrderDate { get; set; } = DateTime.UtcNow;
}

// =====================================================
// REQUEST MODELS
// =====================================================

public record LoginRequest(string Email, string Password);
public record OrderRequest(int UserId, int ProductId, int Quantity);
public record CheckoutRequest(int UserId, CheckoutItem[] Items);
public record CheckoutItem(int ProductId, int Quantity);