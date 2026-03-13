/*
 * Lang-Ango .NET StartupHook - Real DiagnosticSource Implementation
 * 
 * This is loaded via DOTNET_STARTUP_HOOKS environment variable.
 * Subscribes to DiagnosticSource to capture real spans and sends them
 * to the C++ bridge via P/Invoke.
 * 
 * Usage:
 *   export DOTNET_STARTUP_HOOKS=/path/to/LangAngo.StartupHook.dll
 *   export LD_LIBRARY_PATH=/path/to/startup-hook:$LD_LIBRARY_PATH
 *   dotnet run
 */

using System;
using System.Diagnostics;
using System.Runtime.InteropServices;
using System.Threading;

public class LangAngoStartupHook
{
    private static bool _initialized = false;
    private static readonly object _lock = new object();
    
    // P/Invoke to C++ bridge
    [DllImport("liblangango_bridge.so", CallingConvention = CallingConvention.Cdecl, CharSet = CharSet.Ansi)]
    private static extern void langango_bridge_init(string socketPath);
    
    [DllImport("liblangango_bridge.so", CallingConvention = CallingConvention.Cdecl, CharSet = CharSet.Ansi)]
    private static extern void langango_bridge_send_span(
        string traceId, 
        string spanId,
        string parentSpanId,
        string operationName,
        long startTimeNs,
        long endTimeNs,
        int threadId
    );
    
    [DllImport("liblangango_bridge.so", CallingConvention = CallingConvention.Cdecl, CharSet = CharSet.Ansi)]
    private static extern int langango_bridge_should_capture_stack();
    
    [DllImport("liblangango_bridge.so", CallingConvention = CallingConvention.Cdecl)]
    private static extern int langango_bridge_is_enabled();
    
    [DllImport("liblangango_bridge.so", CallingConvention = CallingConvention.Cdecl)]
    private static extern void langango_bridge_shutdown();
    
    public static void Initialize()
    {
        lock (_lock)
        {
            if (_initialized) return;
            _initialized = true;
        }
        
        Console.WriteLine("[LangAngo] StartupHook initializing...");
        
        try
        {
            // Initialize C++ bridge
            string socketPath = "/tmp/langango.sock";
            langango_bridge_init(socketPath);
            Console.WriteLine("[LangAngo] Bridge initialized on " + socketPath);
            
            // Subscribe to DiagnosticSource
            SubscribeToDiagnostics();
            
            Console.WriteLine("[LangAngo] DiagnosticSource subscribed - ready for real spans!");
        }
        catch (Exception ex)
        {
            Console.WriteLine("[LangAngo] Failed to initialize: " + ex.Message);
        }
    }
    
    private static void SubscribeToDiagnostics()
    {
        // Subscribe to all diagnostic listeners
        DiagnosticListener.AllListeners.Subscribe(observer);
    }
    
    private static readonly IObserver<DiagnosticListener> observer = new DiagnosticListenerObserver();

    private class DiagnosticListenerObserver : IObserver<DiagnosticListener>
    {
        public void OnCompleted() { }
        public void OnError(Exception error) { }
        
        public void OnNext(DiagnosticListener listener)
        {
            // Only subscribe to known listeners
            string name = listener.Name;
            
            if (name == "Microsoft.AspNetCore" ||
                name == "System.Net.Http" ||
                name == "Microsoft.EntityFrameworkCore" ||
                name.StartsWith("System.Net"))
            {
                Console.WriteLine("[LangAngo] Subscribing to: " + name);
                
                listener.Subscribe(new DiagnosticEventObserver(name));
            }
        }
    }
    
    private class DiagnosticEventObserver : IObserver<KeyValuePair<string, object>>
    {
        private readonly string _sourceName;
        
        public DiagnosticEventObserver(string sourceName)
        {
            _sourceName = sourceName;
        }
        
        public void OnCompleted() { }
        public void OnError(Exception error) { }
        
        public void OnNext(KeyValuePair<string, object> evnt)
        {
            try
            {
                var activity = Activity.Current;
                if (activity == null) return;
                
                string key = evnt.Key;
                
                // ASP.NET Core HTTP requests
                if (_sourceName == "Microsoft.AspNetCore")
                {
                    if (key == "Microsoft.AspNetCore.Hosting.HttpRequestIn.Start")
                    {
                        OnRequestStart(activity, evnt.Value);
                    }
                    else if (key == "Microsoft.AspNetCore.Hosting.HttpRequestIn.Stop")
                    {
                        OnRequestStop(activity, evnt.Value);
                    }
                }
                
                // System.Net.Http HTTP client calls
                else if (_sourceName == "System.Net.Http")
                {
                    if (key == "System.Net.Http.HttpRequestOut.Start")
                    {
                        OnHttpOutStart(activity, evnt.Value);
                    }
                    else if (key == "System.Net.Http.HttpRequestOut.Stop")
                    {
                        OnHttpOutStop(activity, evnt.Value);
                    }
                }
                
                // Entity Framework Core (database)
                else if (_sourceName == "Microsoft.EntityFrameworkCore")
                {
                    if (key == "Microsoft.EntityFrameworkCore.Database.Command.Start")
                    {
                        OnDbStart(activity, evnt.Value);
                    }
                    else if (key == "Microsoft.EntityFrameworkCore.Database.Command.Stop")
                    {
                        OnDbStop(activity, evnt.Value);
                    }
                }
            }
            catch (Exception ex)
            {
                // Don't let exceptions crash the app
                Console.WriteLine("[LangAngo] Error processing event: " + ex.Message);
            }
        }
    }
    
    private static void OnRequestStart(Activity activity, object payload)
    {
        if (!langango_bridge_is_enabled()) return;
        
        // Store start time in tags for duration calculation
        activity.AddTag("langango.startTime", DateTime.UtcNow.Ticks);
        
        // Get request path
        string operationName = activity.OperationName ?? "HTTP";
        
        // Try to get the actual URL/path
        if (payload is IDisposable disposable)
        {
            // Try to extract request details from payload
        }
        
        Console.WriteLine("[LangAngo] Request START: " + operationName);
    }
    
    private static void OnRequestStop(Activity activity, object payload)
    {
        if (!langango_bridge_is_enabled()) return;
        
        var startTimeObj = activity.GetTag("langango.startTime");
        if (startTimeObj == null) return;
        
        long startTimeTicks = (long)startTimeObj;
        long endTimeTicks = DateTime.UtcNow.Ticks;
        long startTimeNs = startTimeTicks * 100;
        long endTimeNs = endTimeTicks * 100;
        
        string operationName = GetOperationName(activity);
        
        // Convert IDs to hex strings
        string traceId = activity.Id ?? activity.TraceId.ToHexString();
        string spanId = activity.SpanId.ToHexString();
        string parentSpanId = activity.ParentId ?? (activity.ParentSpanId.IsEmpty ? "" : activity.ParentSpanId.ToHexString());
        
        // Send to bridge
        langango_bridge_send_span(
            traceId,
            spanId,
            parentSpanId,
            operationName,
            startTimeNs,
            endTimeNs,
            Thread.CurrentThread.ManagedThreadId
        );
        
        // Check if we should capture stack
        if (langango_bridge_should_capture_stack() == 1)
        {
            Console.WriteLine("[LangAngo] Stack capture requested for: " + operationName);
            // Stack capture would be triggered here in full implementation
        }
        
        Console.WriteLine("[LangAngo] Request STOP: " + operationName);
    }
    
    private static void OnHttpOutStart(Activity activity, object payload)
    {
        if (!langango_bridge_is_enabled()) return;
        
        activity.AddTag("langango.startTime", DateTime.UtcNow.Ticks);
        activity.AddTag("langango.isOutgoing", "true");
    }
    
    private static void OnHttpOutStop(Activity activity, object payload)
    {
        if (!langango_bridge_is_enabled()) return;
        
        var startTimeObj = activity.GetTag("langango.startTime");
        if (startTimeObj == null) return;
        
        long startTimeNs = (long)startTimeObj * 100;
        long endTimeNs = DateTime.UtcNow.Ticks * 100;
        
        string operationName = "HTTP_CLIENT " + (activity.OperationName ?? "Request");
        
        langango_bridge_send_span(
            activity.Id ?? activity.TraceId.ToHexString(),
            activity.SpanId.ToHexString(),
            activity.ParentId ?? "",
            operationName,
            startTimeNs,
            endTimeNs,
            Thread.CurrentThread.ManagedThreadId
        );
    }
    
    private static void OnDbStart(Activity activity, object payload)
    {
        if (!langango_bridge_is_enabled()) return;
        
        activity.AddTag("langango.startTime", DateTime.UtcNow.Ticks);
    }
    
    private static void OnDbStop(Activity activity, object payload)
    {
        if (!langango_bridge_is_enabled()) return;
        
        var startTimeObj = activity.GetTag("langango.startTime");
        if (startTimeObj == null) return;
        
        long startTimeNs = (long)startTimeObj * 100;
        long endTimeNs = DateTime.UtcNow.Ticks * 100;
        
        string operationName = "DB " + (activity.OperationName ?? "Query");
        
        langango_bridge_send_span(
            activity.Id ?? activity.TraceId.ToHexString(),
            activity.SpanId.ToHexString(),
            activity.ParentId ?? "",
            operationName,
            startTimeNs,
            endTimeNs,
            Thread.CurrentThread.ManagedThreadId
        );
    }
    
    private static string GetOperationName(Activity activity)
    {
        // Try to get the display name
        string name = activity.DisplayName;
        if (!string.IsNullOrEmpty(name)) return name;
        
        // Fall back to operation name
        name = activity.OperationName;
        if (!string.IsNullOrEmpty(name)) return name;
        
        // Fall back to kind
        return activity.Kind.ToString();
    }
}

// Entry point called by .NET runtime
public static class EntryPoint
{
    public static void Initialize()
    {
        LangAngoStartupHook.Initialize();
    }
}
