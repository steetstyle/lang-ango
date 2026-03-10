using System.Runtime.CompilerServices;
using Microsoft.AspNetCore.Mvc;
using Npgsql;
using Infrastructure.Common.Exceptions;

var builder = WebApplication.CreateBuilder(args);

builder.Services.AddControllers();
builder.Services.AddSingleton<DbClient>();

var app = builder.Build();

app.MapControllers();

app.Run();

[ApiController]
[Route("api/auto")]
public class AutoInformationController : ControllerBase
{
    private readonly DbClient _db;
    private readonly HttpClient _httpClient;

    public AutoInformationController(DbClient db)
    {
        _db = db;
        _httpClient = new HttpClient();
    }

    [HttpGet("auto-information")]
    public async Task<IActionResult> GetAutoInformation()
    {
        // 1. eBPF tarafından yakalanacak SQL sorguları
        await _db.ExecuteAsync("SELECT * FROM q.token AS t WHERE t.jwt_token = @_tokenString_0 AND t.is_active = TRUE");
        await _db.ExecuteAsync("SELECT * FROM public.\"user\" WHERE id = @Id");
        await _db.ExecuteAsync("INSERT INTO log.inbound_request_log(id, payload) VALUES (1, 'test')");

        // 2. .NET profiler tarafından yakalanacak iç metod
        QITSP_GetResource();

        await _db.ExecuteAsync("SELECT * FROM cache.sbm_tescil_sorgu_value AS s WHERE s.id = @Id");

        // 3. eBPF TLS uprobe ile yakalanacak harici çağrı
        await _httpClient.GetAsync("https://qapi.quickisgorta.com/sbm/tnb/aktif-tescil-10");

        await _db.ExecuteAsync("UPDATE log.inbound_request_log SET payload = 'updated' WHERE id = 1");

        // 4. Özel exception
        throw new SbmException("Header elamanının formatı hatalıdır.");
    }

    [MethodImpl(MethodImplOptions.NoInlining)]
    private void QITSP_GetResource()
    {
        Thread.Sleep(5);
    }
}

public class DbClient
{
    private readonly string _connectionString;

    public DbClient(IConfiguration configuration)
    {
        _connectionString = configuration.GetConnectionString("Default") ??
                            "Host=localhost;Port=5432;Username=postgres;Password=postgres;Database=autoapi";
    }

    public async Task ExecuteAsync(string sql)
    {
        await using var conn = new NpgsqlConnection(_connectionString);
        await conn.OpenAsync();
        await using var cmd = new NpgsqlCommand(sql, conn);
        await cmd.ExecuteNonQueryAsync();
    }
}

namespace Infrastructure.Common.Exceptions
{
    public class SbmException : Exception
    {
        public SbmException(string message) : base(message)
        {
        }
    }
}

