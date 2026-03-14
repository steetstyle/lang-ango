var builder = WebApplication.CreateBuilder(args);
var app = builder.Build();

// Simulate slow database query
app.MapGet("/api/data", async (HttpContext context) =>
{
    var stopwatch = System.Diagnostics.Stopwatch.StartNew();
    
    // 5% chance of slow response (anomaly simulation)
    if (Random.Shared.Next(1, 100) <= 5)
    {
        Console.WriteLine("[SAMPLE] Slow request detected (anomaly)!");
        await Task.Delay(2000); // 2 second delay
    }
    else
    {
        await Task.Delay(Random.Shared.Next(5, 50)); // Normal: 5-50ms
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

// Simulate database call
app.MapGet("/api/db/{id}", async (int id) =>
{
    // Simulate DB query
    await Task.Delay(Random.Shared.Next(10, 100));
    
    return Results.Ok(new
    {
        id = id,
        name = $"User {id}",
        email = $"user{id}@example.com"
    });
});

app.Run();
