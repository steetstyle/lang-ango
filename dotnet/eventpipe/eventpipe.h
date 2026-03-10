/*
 * Lang-Ango EventPipe Reader
 * Reads .NET EventPipe for symbol resolution and method events
 */

#ifndef LANG_ANGO_EVENTPIPE_H
#define LANG_ANGO_EVENTPIPE_H

#include <string>
#include <vector>
#include <map>
#include <memory>
#include <functional>

#ifdef _WIN32
#include <Windows.h>
#else
#include <sys/uio.h>
#include <unistd.h>
#endif

namespace langango {
namespace dotnet {

enum class EventPipeEventType {
    MethodEnter = 0,
    MethodLeave = 1,
    GCStart = 2,
    GCEnd = 3,
    Exception = 4,
    ThreadCreated = 5,
    ThreadDestroyed = 6,
    ModuleLoaded = 7,
    MethodILMap = 8,
};

struct EventPipeMethodEvent {
    uint64_t timestamp_ns;
    uint32_t thread_id;
    uint64_t method_id;
    uint64_t method_start_address;
    uint32_t method_token;
    uint32_t stack_depth;
    std::vector<uint64_t> stack_frames;
};

struct EventPipeExceptionEvent {
    uint64_t timestamp_ns;
    uint32_t thread_id;
    std::string exception_type;
    std::string message;
    std::vector<std::string> stack_trace;
};

struct EventPipeModuleInfo {
    uint64_t module_id;
    std::string name;
    uint64_t base_address;
    uint32_t size;
    std::string assembly_name;
};

struct EventPipeMethodInfo {
    uint64_t method_id;
    uint32_t module_id;
    uint32_t method_token;
    std::string name;
    std::string class_name;
    std::string namespace_name;
    std::string signature;
    uint64_t il_offset;
};

class EventPipeReader {
public:
    using EventCallback = std::function<void(const EventPipeMethodEvent&)>;
    using ExceptionCallback = std::function<void(const EventPipeExceptionEvent&)>;

    EventPipeReader();
    ~EventPipeReader();

    bool StartSession(const std::string& output_path, uint32_t circular_buffer_mb = 1024);
    bool StopSession();
    bool IsSessionActive() const { return session_active_; }

    void SetMethodEventCallback(EventCallback callback);
    void SetExceptionCallback(ExceptionCallback callback);

    std::string ResolveMethodName(uint64_t method_id);
    std::string ResolveStackFrame(uint64_t ip, uint64_t sp);

    bool ReadFromFile(const std::string& nettrace_path);
    bool StartListening(uint16_t port = 9000);

private:
    bool session_active_;
    uint32_t circular_buffer_mb_;
    std::string output_path_;
    
    EventCallback method_event_callback_;
    ExceptionCallback exception_callback_;

    std::map<uint64_t, EventPipeMethodInfo> method_map_;
    std::map<uint64_t, EventPipeModuleInfo> module_map_;
    std::map<uint64_t, std::string> address_to_symbol_;

    void ProcessEventBlock(const uint8_t* data, size_t size);
    void ParseMethodEvent(const uint8_t* payload, size_t size);
    void ParseExceptionEvent(const uint8_t* payload, size_t size);
    void ParseModuleLoadEvent(const uint8_t* payload, size_t size);
    void ParseMethodLoadEvent(const uint8_t* payload, size_t size);

    bool LoadSymbolsFromFile(const std::string& file_path);
    void BuildSymbolCache();
    std::string FindSymbolInCache(uint64_t address);
};

class EventPipeProvider {
public:
    EventPipeProvider(const std::string& name, uint64_t keywords);
    ~EventPipeProvider();

    bool Initialize();
    void Shutdown();

    void SetKeywords(uint64_t keywords);
    void Enable();
    void Disable();

    static uint64_t Keyword_Exception;
    static uint64_t Keyword_Method;
    static uint64_t Keyword_GC;
    static uint64_t Keyword_Threading;
    static uint64_t Keyword_Loader;

private:
    std::string name_;
    uint64_t keywords_;
    void* provider_handle_;
    bool enabled_;
};

class ManagedCallStackResolver {
public:
    ManagedCallStackResolver();
    ~ManagedCallStackResolver();

    bool Initialize();
    void AddModule(uint64_t base_address, const std::string& module_path);
    
    std::vector<std::string> ResolveCallStack(const std::vector<uint64_t>& frames);
    std::string ResolveMethod(uint64_t ip);

private:
    struct ModuleDebugInfo {
        std::string path;
        uint64_t base_address;
        std::vector<uint8_t> pdb_info;
    };

    std::map<uint64_t, ModuleDebugInfo> loaded_modules_;
    bool initialized_;
};

} // namespace dotnet
} // namespace langango

#endif // LANG_ANGO_EVENTPIPE_H
