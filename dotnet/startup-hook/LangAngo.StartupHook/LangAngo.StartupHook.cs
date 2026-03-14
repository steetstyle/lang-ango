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
using System.Collections.Concurrent;
using System.Collections.Generic;
using System.Diagnostics;
using System.Runtime.InteropServices;
using System.Threading;

public class StartupHook
{
    public static void Initialize()
    {
        Console.WriteLine("[LangAngo] StartupHook is starting...");
        LangAngoStartupHook.Initialize();
    }
}

public class LangAngoStartupHook
{
    private static bool _initialized = false;
    private static readonly object _lock = new object();
    
    // Symbol cache: Address -> Method Name (for IP-based symbolication)
    private static readonly ConcurrentDictionary<IntPtr, string> _symbolCache = new();
    
    // P/Invoke to C++ bridge
    [DllImport("liblangango_bridge.so", CallingConvention = CallingConvention.Cdecl, CharSet = CharSet.Ansi, EntryPoint = "langango_bridge_set_socket_path")]
    private static extern void langango_bridge_set_socket_path(string path);
    
    [DllImport("liblangango_bridge.so", CallingConvention = CallingConvention.Cdecl, CharSet = CharSet.Ansi, EntryPoint = "langango_bridge_init")]
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
    private static extern void langango_bridge_send_span_with_stack(
        string traceId, 
        string spanId,
        string parentSpanId,
        string operationName,
        long startTimeNs,
        long endTimeNs,
        int threadId,
        IntPtr stackFrames,
        int frameCount
    );
    
    [DllImport("liblangango_bridge.so", CallingConvention = CallingConvention.Cdecl)]
    private static extern int langango_bridge_should_capture_stack();
    
    [DllImport("liblangango_bridge.so", CallingConvention = CallingConvention.Cdecl)]
    private static extern int langango_bridge_is_enabled();
    
    [DllImport("liblangango_bridge.so", CallingConvention = CallingConvention.Cdecl)]
    private static extern void langango_bridge_shutdown();
    
    // Send symbol update (address -> name mapping) - called once per unique method
    [DllImport("liblangango_bridge.so", CallingConvention = CallingConvention.Cdecl, CharSet = CharSet.Ansi)]
    private static extern void langango_bridge_send_symbol(ulong address, string methodName);
    
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
            string socketPath = Environment.GetEnvironmentVariable("LANGANGO_SOCKET_PATH") ?? "/tmp/langango.sock";
            
            // Set the socket path in the C++ bridge
            langango_bridge_set_socket_path(socketPath);
            
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
        if (langango_bridge_is_enabled() == 0) return;
        
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
        if (langango_bridge_is_enabled() == 0) return;
        
        var startTimeObj = activity.GetTagItem("langango.startTime");
        if (startTimeObj == null) return;
        
        long startTimeTicks = (long)startTimeObj;
        long endTimeTicks = DateTime.UtcNow.Ticks;
        long startTimeNs = startTimeTicks * 100;
        long endTimeNs = endTimeTicks * 100;
        
        string operationName = GetOperationName(activity);
        
        // Use Activity's TraceId and SpanId - these are the real W3C format IDs
        string traceId = activity.TraceId.ToHexString();
        string spanId = activity.SpanId.ToHexString();
        
        // ParentSpanId - only set if there's actually a parent
        string parentSpanId = "";
        if (activity.ParentSpanId != default)
        {
            parentSpanId = activity.ParentSpanId.ToHexString();
        }
        
        Console.WriteLine($"[LangAngo-DEBUG] TraceID={traceId}, SpanID={spanId}, ParentID={parentSpanId}, Op={operationName}");
        
// Check if we should capture stack
        bool shouldCaptureStack = langango_bridge_should_capture_stack() == 1;
        
        Console.WriteLine($"[LangAngo-DEBUG] shouldCaptureStack={shouldCaptureStack}");
        
        if (shouldCaptureStack)
        {
            // Capture stack trace using .NET StackTrace
            try
            {
                var stackTrace = new System.Diagnostics.StackTrace(true);
                var frames = stackTrace.GetFrames();
                int frameCount = frames != null ? frames.Length : 0;
                
                Console.WriteLine($"[LangAngo-DEBUG] frameCount={frameCount}");
                
                if (frameCount > 0)
                {
                    // Allocate native buffer for frame IPs
                    IntPtr frameBuffer = Marshal.AllocHGlobal(frameCount * 8);
                    try
                    {
                        // Get native instruction pointers (skip first 2 frames: this function and OnRequestStop)
                        int framesToSend = Math.Min(frameCount - 2, 16);
                        if (framesToSend > 0)
                        {
                            for (int i = 0; i < framesToSend; i++)
                            {
                                // Get method for each frame
                                var method = frames[i + 2].GetMethod();
                                IntPtr methodAddr = IntPtr.Zero;
                                string methodName = "unknown";
                                
                                if (method != null)
                                {
                                    try
                                    {
                                        // Get actual function pointer
                                        RuntimeMethodHandle handle = method.MethodHandle;
                                        methodAddr = handle.GetFunctionPointer();
                                        
                                        // Register symbol with caching
                                        methodName = RegisterMethodSymbol(methodAddr, method);
                                    }
                                    catch (Exception ex)
                                    {
                                        // Fallback to metadata token as pseudo-IP
                                        // Note: GetFunctionPointer may fail for some dynamic methods
                                        Console.Error.WriteLine($"[LangAngo-WARN] GetFunctionPointer failed: {method.Name} -> using metadata token, error: {ex.Message}");
                                        methodAddr = (IntPtr)((ulong)method.MetadataToken);
                                        methodName = method.DeclaringType?.FullName != null 
                                            ? method.DeclaringType.FullName + "." + method.Name 
                                            : method.Name;
                                        
                                        // Register symbol using metadata token address
                                        _symbolCache.TryAdd(methodAddr, methodName);
                                        langango_bridge_send_symbol((ulong)methodAddr.ToInt64(), methodName);
                                        methodName = _symbolCache[methodAddr];
                                    }
                                }
                                
                                // Write address to buffer
                                Marshal.WriteInt64(frameBuffer, i * 8, methodAddr.ToInt64());
                                Console.WriteLine($"[LangAngo-FRAME] Frame {i}: addr=0x{methodAddr.ToInt64():X}, name={methodName}");
                            }
                            
                            // Send span with stack frames
                            langango_bridge_send_span_with_stack(
                                traceId,
                                spanId,
                                parentSpanId,
                                operationName,
                                startTimeNs,
                                endTimeNs,
                                Thread.CurrentThread.ManagedThreadId,
                                frameBuffer,
                                framesToSend
                            );
                            
                            Console.WriteLine($"[LangAngo] Captured {framesToSend} stack frames for: {operationName}");
                            return;
                        }
                    }
                    finally
                    {
                        Marshal.FreeHGlobal(frameBuffer);
                    }
                }
            }
            catch (Exception ex)
            {
                Console.WriteLine("[LangAngo] Stack capture error: " + ex.Message);
            }
        }
        
        // Fallback: send without stack frames
        langango_bridge_send_span(
            traceId,
            spanId,
            parentSpanId,
            operationName,
            startTimeNs,
            endTimeNs,
            Thread.CurrentThread.ManagedThreadId
        );
        
        Console.WriteLine("[LangAngo] Request STOP: " + operationName);
    }
    
    private static void OnHttpOutStart(Activity activity, object payload)
    {
        if (langango_bridge_is_enabled() == 0) return;
        
        activity.AddTag("langango.startTime", DateTime.UtcNow.Ticks);
        activity.AddTag("langango.isOutgoing", "true");
    }
    
    private static void OnHttpOutStop(Activity activity, object payload)
    {
        if (langango_bridge_is_enabled() == 0) return;
        
        var startTimeObj = activity.GetTagItem("langango.startTime");
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
        if (langango_bridge_is_enabled() == 0) return;
        
        activity.AddTag("langango.startTime", DateTime.UtcNow.Ticks);
    }
    
    private static void OnDbStop(Activity activity, object payload)
    {
        if (langango_bridge_is_enabled() == 0) return;
        
        var startTimeObj = activity.GetTagItem("langango.startTime");
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
    
    // Register a method address and send symbol update if new
    // Returns the method name (either cached or newly resolved)
    private static string RegisterMethodSymbol(IntPtr methodAddr, System.Reflection.MethodBase? method)
    {
        // Check if we already have this symbol cached
        if (_symbolCache.TryGetValue(methodAddr, out string? cachedName))
        {
            return cachedName;
        }
        
        // Resolve method name
        string fullName;
        if (method != null && method.DeclaringType != null)
        {
            fullName = method.DeclaringType.FullName + "." + method.Name;
        }
        else if (method != null)
        {
            fullName = method.Name;
        }
        else
        {
            fullName = "UnknownMethod";
        }
        
        // Cache it
        _symbolCache.TryAdd(methodAddr, fullName);
        
        // Send symbol update to Go agent (first time only)
        try
        {
            langango_bridge_send_symbol((ulong)methodAddr.ToInt64(), fullName);
            Console.WriteLine($"[LangAngo-SYMBOL] New symbol registered: addr={methodAddr:X}, name={fullName}");
        }
        catch (Exception ex)
        {
            Console.WriteLine($"[LangAngo-SYMBOL] Failed to send: {ex.Message}");
        }
        
        return fullName;
    }
}
