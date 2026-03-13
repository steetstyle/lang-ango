/*
 * Lang-Ango .NET Profiler Agent
 * Implements ICorProfilerCallback for method instrumentation
 * 
 * This is a C++ shared library that gets injected into .NET processes
 * to capture method entry/exit with full stack traces and timing.
 * 
 * Supports Selective Sampling for Dynatrace-level APM:
 * - Slow request detection (>2s)
 * - Exception-based sampling
 * - Thread call stack sampling
 */

#ifndef LANG_ANGO_PROFILER_H
#define LANG_ANGO_PROFILER_H

#include <corhdr.h>
#include <corprof.h>
#include <atomic>
#include <chrono>
#include <map>
#include <mutex>
#include <string>
#include <vector>
#include <queue>
#include <thread>
#include <condition_variable>
#include <unordered_map>

#ifdef _WIN32
#define PROFILER_EXPORT __declspec(dllexport)
#else
#define PROFILER_EXPORT __attribute__((visibility("default")))
#endif

namespace langango {
namespace dotnet {

// ============================================================================
// Data Structures for Selective Sampling
// ============================================================================

struct MethodInfo {
    std::wstring className;
    std::wstring methodName;
    std::wstring signature;
    ModuleID moduleId;
    mdToken methodToken;
    ULONG64 methodId;
};

struct MethodCallEvent {
    uint32_t threadId;
    uint64_t timestamp_ns;
    uint64_t methodId;
    uint64_t duration_ns;
    uint32_t depth;
    bool isEntry;
    std::vector<uint64_t> callStack;
};

struct ExceptionEvent {
    uint32_t threadId;
    uint64_t timestamp_ns;
    std::wstring exceptionType;
    std::wstring message;
    std::vector<std::wstring> stackTrace;
};

// ============================================================================
// Thread ↔ Trace Correlation (Critical for Dynatrace-level performance)
// ============================================================================

struct TraceContext {
    ActivityTraceId traceId;
    ActivitySpanId spanId;
    uint64_t startTime_ns;
    std::string operationName;
    bool isRootSpan;
};

// ============================================================================
// Stack Frame (for call stack sampling)
// ============================================================================

struct StackFrame {
    uint64_t ip;           // Instruction pointer
    uint64_t sp;           // Stack pointer
    uint64_t methodId;     // .NET method ID
    std::wstring methodName;
    std::wstring moduleName;
    std::string fileName;
    uint32_t lineNumber;
};

// ============================================================================
// IPC Message Types (for Go Agent Communication)
// ============================================================================

enum class IPCMessageType : uint8_t {
    SpanWithStack = 1,
    TraceContext = 2,
    ThreadSample = 3,
    Exception = 4,
    Heartbeat = 5
};

struct IPCMessageHeader {
    uint32_t magic;           // 0x4C414E47 ("LANG")
    uint16_t version;         // 1
    IPCMessageType type;
    uint32_t payloadSize;
    uint64_t sequence;
};

struct SpanWithStackData {
    char traceId[32];         // Hex string
    char spanId[16];         // Hex string
    char parentSpanId[16];   // Hex string
    char operationName[256];
    uint64_t startTime_ns;
    uint64_t endTime_ns;
    uint32_t threadId;
    uint32_t stackFrameCount;
    // Followed by StackFrame[stackFrameCount]
};

struct ThreadSampleData {
    char traceId[32];         // Hex string (empty if no active trace)
    uint32_t osThreadId;
    uint32_t stackFrameCount;
    uint64_t timestamp_ns;
    // Followed by StackFrame[stackFrameCount]
};

// ============================================================================
// Configuration (set from Go Agent)
// ============================================================================

struct ProfilerConfig {
    uint32_t slowRequestThresholdMs = 2000;    // 2 seconds (Dynatrace standard)
    bool captureOnException = true;
    uint32_t samplingIntervalMs = 100;         // 100ms stack sampling
    uint32_t maxStackDepth = 64;
    uint32_t maxTrackedTraces = 1000;
    char socketPath[256] = "/tmp/langango.sock";
    bool enableDebugLogging = false;
};

// ============================================================================
// Main Profiler Agent Class
// ============================================================================

class ProfilerAgent : public ICorProfilerCallback8 {
public:
    ProfilerAgent();
    ~ProfilerAgent();

    // IUnknown
    HRESULT STDMETHODCALLTYPE QueryInterface(REFIID riid, void** ppvObject) override;
    ULONG STDMETHODCALLTYPE AddRef() override;
    ULONG STDMETHODCALLTYPE Release() override;

    // ICorProfilerCallback
    HRESULT STDMETHODCALLTYPE Initialize(IUnknown* pCorDebugInfo) override;
    HRESULT STDMETHODCALLTYPE Shutdown() override;
    HRESULT STDMETHODCALLTYPE AppDomainCreationStarted(AppDomainID appDomainId) override;
    HRESULT STDMETHODCALLTYPE AppDomainCreationFinished(AppDomainID appDomainId, HRESULT hrStatus) override;
    HRESULT STDMETHODCALLTYPE AppDomainShutdownStarted(AppDomainID appDomainId) override;
    HRESULT STDMETHODCALLTYPE AppDomainShutdownFinished(AppDomainID appDomainId, HRESULT hrStatus) override;
    HRESULT STDMETHODCALLTYPE AssemblyLoadStarted(AssemblyID assemblyId) override;
    HRESULT STDMETHODCALLTYPE AssemblyLoadFinished(AssemblyID assemblyId, HRESULT hrStatus) override;
    HRESULT STDMETHODCALLTYPE AssemblyUnloadStarted(AssemblyID assemblyId) override;
    HRESULT STDMETHODCALLTYPE AssemblyUnloadFinished(AssemblyID assemblyId, HRESULT hrStatus) override;
    HRESULT STDMETHODCALLTYPE ModuleLoadStarted(ModuleID moduleId) override;
    HRESULT STDMETHODCALLTYPE ModuleLoadFinished(ModuleID moduleId, HRESULT hrStatus) override;
    HRESULT STDMETHODCALLTYPE ModuleUnloadStarted(ModuleID moduleId) override;
    HRESULT STDMETHODCALLTYPE ModuleUnloadFinished(ModuleID moduleId, HRESULT hrStatus) override;
    HRESULT STDMETHODCALLTYPE ModuleAttachedToAssembly(ModuleID moduleId, AssemblyID assemblyId) override;
    HRESULT STDMETHODCALLTYPE ClassLoadStarted(ClassID classId) override;
    HRESULT STDMETHODCALLTYPE ClassLoadFinished(ClassID classId, HRESULT hrStatus) override;
    HRESULT STDMETHODCALLTYPE ClassUnloadStarted(ClassID classId) override;
    HRESULT STDMETHODCALLTYPE ClassUnloadFinished(ClassID classId, HRESULT hrStatus) override;
    HRESULT STDMETHODCALLTYPE FunctionUnloadStarted(FunctionID functionId) override;
    HRESULT STDMETHODCALLTYPE JITCompilationStarted(FunctionID functionId, BOOL fIsSafeToBlock) override;
    HRESULT STDMETHODCALLTYPE JITCompilationFinished(FunctionID functionId, HRESULT hrStatus, BOOL fIsSafeToBlock) override;
    HRESULT STDMETHODCALLTYPE JITCachedFunctionSearchStarted(FunctionID functionId, BOOL* pbUseCachedFunction) override;
    HRESULT STDMETHODCALLTYPE JITCachedFunctionSearchFinished(FunctionID functionId, HRESULT hrStatus) override;
    HRESULT STDMETHODCALLTYPE JITFunctionPitched(FunctionID functionId) override;
    HRESULT STDMETHODCALLTYPE JITInlining(FunctionID callerId, FunctionID calleeId, BOOL* pfShouldInline) override;
    HRESULT STDMETHODCALLTYPE ThreadCreated(ThreadID threadId) override;
    HRESULT STDMETHODCALLTYPE ThreadDestroyed(ThreadID threadId) override;
    HRESULT STDMETHODCALLTYPE ThreadAssignedToOSThread(ThreadID managedThreadId, DWORD osThreadId) override;
    HRESULT STDMETHODCALLTYPE RemotingClientInvocationStarted() override;
    HRESULT STDMETHODCALLTYPE RemotingClientSendingMessage(GUID* pCookie, BOOL fIsAsync) override;
    HRESULT STDMETHODCALLTYPE RemotingClientReceivingReply(GUID* pCookie, BOOL fIsAsync) override;
    HRESULT STDMETHODCALLTYPE RemotingServerInvocationStarted() override;
    HRESULT STDMETHODCALLTYPE RemotingServerReceivingMessage(GUID* pCookie, BOOL fIsAsync, BOOL fIsOutgoing) override;
    HRESULT STDMETHODCALLTYPE RemotingServerSendingReply(GUID* pCookie, BOOL fIsAsync, IMethodMessage* pReply) override;
    HRESULT STDMETHODCALLTYPE UnmanagedToManagedTransition(ThreadID threadId, COR_PRF_TRANSITION_REASON reason) override;
    HRESULT STDMETHODCALLTYPE ManagedToUnmanagedTransition(ThreadID threadId, COR_PRF_TRANSITION_REASON reason) override;
    HRESULT STDMETHODCALLTYPE RuntimeSuspendStarted(COR_PRF_SUSPEND_REASON suspendReason) override;
    HRESULT STDMETHODCALLTYPE RuntimeSuspendFinished() override;
    HRESULT STDMETHODCALLTYPE RuntimeSuspendAborted() override;
    HRESULT STDMETHODCALLTYPE RuntimeResumeStarted() override;
    HRESULT STDMETHODCALLTYPE RuntimeResumeFinished() override;
    HRESULT STDMETHODCALLTYPE RuntimeThreadSuspended(ThreadID threadId) override;
    HRESULT STDMETHODCALLTYPE RuntimeThreadResumed(ThreadID threadId) override;
    HRESULT STDMETHODCALLTYPE MovedReferences(ULONG cMovedObjectIDRanges, ObjectID* pOldObjectIDRangeStart, ObjectID* pNewObjectIDRangeStart, ULONG* pObjectIDRangeLength) override;
    HRESULT STDMETHODCALLTYPE ObjectAllocatedByClass(ObjectID objectId, ClassID classId) override;
    HRESULT STDMETHODCALLTYPE ObjectAllocated(ObjectID objectId, ClassID classId) override;
    HRESULT STDMETHODCALLTYPE ObjectReferences(ObjectID objectId, ClassID classId, ULONG cObjectRefs, ObjectID* pObjectRefs) override;
    HRESULT STDMETHODCALLTYPE RootReferences(ULONG cRootRefs, ObjectID* pRootRefs, COR_PRF_GC_ROOT_KIND* pRootKinds, UINT32* pRootIds, ULONG* pRootRefFlags) override;
    HRESULT STDMETHODCALLTYPE ExceptionThrown(ObjectID thrownObjectId) override;
    HRESULT STDMETHODCALLTYPE ExceptionSearchFunctionEnter(FunctionID functionId) override;
    HRESULT STDMETHODCALLTYPE ExceptionSearchFunctionLeave() override;
    HRESULT STDMETHODCALLTYPE ExceptionSearchFilterEnter(FunctionID functionId) override;
    HRESULT STDMETHODCALLTYPE ExceptionSearchFilterLeave() override;
    HRESULT STDMETHODCALLTYPE ExceptionSearchCatcherFound(FunctionID functionId) override;
    HRESULT STDMETHODCALLTYPE ExceptionOSHandlerEnter(void* __formal) override;
    HRESULT STDMETHODCALLTYPE ExceptionOSHandlerLeave(void* __formal) override;
    HRESULT STDMETHODCALLTYPE ExceptionUnwindFunctionEnter(FunctionID functionId) override;
    HRESULT STDMETHODCALLTYPE ExceptionUnwindFunctionLeave() override;
    HRESULT STDMETHODCALLTYPE ExceptionCatcherEnter(FunctionID functionId, ObjectID objectId) override;
    HRESULT STDMETHODCALLTYPE ExceptionCatcherLeave() override;
    HRESULT STDMETHODCALLTYPE COMClassicVTableCreated(ClassID classId, REFGUID implementedIID, void* pVTable, ULONG cSlots) override;
    HRESULT STDMETHODCALLTYPE COMClassicVTableDestroyed(ClassID classId, REFGUID implementedIID) override;
    HRESULT STDMETHODCALLTYPE ThreadNameChanged(ThreadID threadId, ULONG cchName, WCHAR* name) override;
    HRESULT STDMETHODCALLTYPE InitializeForAttach(IUnknown* pCorDebugInfo, void* pvClientData, UINT cbClientData) override;
    HRESULT STDMETHODCALLTYPE ProfilerAttachComplete() override;
    HRESULT STDMETHODCALLTYPE ProfilerDetachSucceeded() override;
    HRESULT STDMETHODCALLTYPE ForceGC() override;
    HRESULT STDMETHODCALLTYPE StringRefTypeID(StringObjectID stringId, TypeID* pTypeId) override;
    HRESULT STDMETHODCALLTYPE ObjectRefTypeID(ObjectID objectId, TypeID* pTypeId) override;
    HRESULT STDMETHODCALLTYPE ObjectCreatedByClass(ObjectID objectId, ClassID classId) override;
    HRESULT STDMETHODCALLTYPE ObjectDestroyed(ObjectID objectId) override;
    HRESULT STDMETHODCALLTYPE RootReferences2(ULONG cRootRefs, ObjectID* pRootRefs, COR_PRF_GC_ROOT_KIND* pRootKinds, UINT32* pRootIds, ULONG* pRootRefFlags, UINT64* pRootScope) override;
    HRESULT STDMETHODCALLTYPE HandleCreated(GCHandleID objectId, ObjectID initialObjectId) override;
    HRESULT STDMETHODCALLTYPE HandleDestroyed(GCHandleID objectId) override;
    HRESULT STDMETHODCALLTYPE ClassIDName2(ClassID classId, ULONG* pchName, __out_ecount_part_opt(*pchName, *pchName) WCHAR* szName) override;
    HRESULT STDMETHODCALLTYPE ClassIDModule2(ClassID classId, AppDomainID* pAppDomainId, ModuleID* pModuleId) override;
    HRESULT STDMETHODCALLTYPE GetClassIDInfo2(ClassID classId, ModuleID* pModuleId, mdTypeDef* pTypeDefToken, ArrayClassID* pArrayClass, ULONG* cNumTypeArgs, TypeID* typeArgs) override;
    HRESULT STDMETHODCALLTYPE GetFunctionInfo2(FunctionID functionId, AppDomainID* pAppDomainId, ModuleID* pModuleId, mdToken* pToken, TypeID* pClassId, ULONG* cNumTypeArgs, TypeID* typeArgs, ULONG* cNumGenericArgs, TypeID* genericArgs) override;
    HRESULT STDMETHODCALLTYPE GetFunctionPointerInfo2(FunctionID functionId, AppDomainID* pAppDomainId, ModuleID* pModuleId, mdToken* pToken, TypeID* pClassId, ULONG* cNumTypeArgs, TypeID* typeArgs, ULONG* cNumGenericArgs, TypeID* genericArgs, void* ppFunctionPointer) override;
    HRESULT STDMETHODCALLTYPE JITlining(FunctionID functionId, BOOL* pfShouldInline) override;
    HRESULT STDMETHODCALLTYPE PostJITCompilationStarted(FunctionID functionId, BOOL fIsSafeToBlock) override;
    HRESULT STDMETHODCALLTYPE PostJITFunctionPitched(FunctionID functionId) override;

    // Configuration API (called from Go Agent)
    void SetConfig(const ProfilerConfig& config);
    ProfilerConfig GetConfig() const;

private:
    std::atomic<ULONG> m_refCount;
    ICorProfilerInfo* m_profilerInfo;
    bool m_initialized;
    bool m_instrumentationEnabled;
    
    // Configuration
    ProfilerConfig m_config;

    // Method tracking
    std::mutex m_methodMapMutex;
    std::map<FunctionID, MethodInfo> m_methodMap;
    
    // Event buffer
    std::mutex m_eventBufferMutex;
    std::vector<MethodCallEvent> m_eventBuffer;
    
    // Sequence counter
    std::atomic<uint64_t> m_eventSequence;
    
    // =========================================================================
    // Selective Sampling - Thread ↔ Trace Correlation
    // =========================================================================
    
    // Managed Thread ID → OS Thread ID mapping
    std::mutex m_threadMapMutex;
    std::unordered_map<ThreadID, DWORD> m_managedToOSThreadMap;
    
    // OS Thread ID → TraceContext (for quick lookup during sampling)
    std::mutex m_traceContextMapMutex;
    std::unordered_map<DWORD, TraceContext> m_osThreadToTraceMap;
    
    // Active traces being tracked (TraceId string → start time)
    std::mutex m_activeTracesMutex;
    std::unordered_map<std::string, uint64_t> m_activeTraces;  // traceId → startTime
    
    // Traces selected for frequent sampling
    std::mutex m_selectedTracesMutex;
    std::unordered_set<std::string> m_selectedTraces;  // traceIds being sampled
    
    // Request timers (spanId → start time)
    std::mutex m_requestTimersMutex;
    std::unordered_map<std::string, uint64_t> m_requestTimers;
    
    // Exception flags per trace
    std::mutex m_exceptionFlagsMutex;
    std::unordered_set<std::string> m_tracesWithExceptions;
    
    // Sampling thread
    std::thread m_samplingThread;
    std::atomic<bool> m_samplingEnabled;
    std::condition_variable m_samplingCV;
    
    // IPC
    int m_socketFd;
    std::thread m_ipcThread;
    std::atomic<bool> m_ipcConnected;
    
    // Private methods
    void ProcessMethodEnter(ThreadID threadId, FunctionID functionId);
    void ProcessMethodLeave(ThreadID threadId, FunctionID functionId, uint64_t duration_ns);
    void ProcessException(ExceptionEvent& event);
    std::wstring GetMethodName(FunctionID functionId);
    std::vector<uint64_t> CaptureCallStack();
    void SendEventsToAgent();
    
    // Selective Sampling methods
    void OnThreadAssignedToOSThreadInternal(ThreadID managedThreadId, DWORD osThreadId);
    void StartTraceContext(ActivityTraceId traceId, ActivitySpanId spanId, const char* operationName);
    void EndTraceContext(const char* traceId);
    void OnException(const char* traceId);
    bool ShouldSampleTrace(const char* traceId);
    
    // Sampling thread
    void SamplingThreadLoop();
    void CaptureAndSendThreadSamples();
    
    // IPC
    bool ConnectToAgent();
    void IPCThreadLoop();
    bool SendIPCMessage(const void* data, size_t size);
    void FormatTraceId(char* buffer, size_t size, ActivityTraceId traceId);
    void FormatSpanId(char* buffer, size_t size, ActivitySpanId spanId);
};

} // namespace dotnet
} // namespace langango

// DLL entry points
extern "C" {
    PROFILER_EXPORT HRESULT GetProfilerClassID(CLSID* pClassId);
    PROFILER_EXPORT HRESULT CreateProfilerInstance(REFIID riid, void** ppv);
}

#endif // LANG_ANGO_PROFILER_H
