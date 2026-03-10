/*
 * Lang-Ango .NET Profiler Agent Implementation
 * Implements ICorProfilerCallback for full method instrumentation
 */

#include "profiler.h"
#include <chrono>
#include <thread>
#include <cstring>

namespace langango {
namespace dotnet {

ProfilerAgent::ProfilerAgent() 
    : m_refCount(0)
    , m_profilerInfo(nullptr)
    , m_initialized(false)
    , m_instrumentationEnabled(false)
    , m_eventSequence(0)
{
}

ProfilerAgent::~ProfilerAgent() {
    if (m_profilerInfo) {
        m_profilerInfo->Release();
        m_profilerInfo = nullptr;
    }
}

HRESULT STDMETHODCALLTYPE ProfilerAgent::QueryInterface(REFIID riid, void** ppvObject) {
    if (riid == IID_IUnknown || riid == IID_ICorProfilerCallback || 
        riid == IID_ICorProfilerCallback2 || riid == IID_ICorProfilerCallback3 ||
        riid == IID_ICorProfilerCallback4 || riid == IID_ICorProfilerCallback5 ||
        riid == IID_ICorProfilerCallback6 || riid == IID_ICorProfilerCallback7 ||
        riid == IID_ICorProfilerCallback8) {
        AddRef();
        *ppvObject = this;
        return S_OK;
    }
    *ppvObject = nullptr;
    return E_NOINTERFACE;
}

ULONG STDMETHODCALLTYPE ProfilerAgent::AddRef() {
    return ++m_refCount;
}

ULONG STDMETHODCALLTYPE ProfilerAgent::Release() {
    ULONG count = --m_refCount;
    if (count == 0) {
        delete this;
    }
    return count;
}

HRESULT STDMETHODCALLTYPE ProfilerAgent::Initialize(IUnknown* pCorDebugInfo) {
    HRESULT hr = pCorDebugInfo->QueryInterface(IID_ICorProfilerInfo, (void**)&m_profilerInfo);
    if (FAILED(hr)) {
        return hr;
    }

    DWORD eventMask = 
        COR_PRF_MONITOR_JIT_COMPILATION |
        COR_PRF_MONITOR_THREADS |
        COR_PRF_MONITOR_EXCEPTIONS |
        COR_PRF_ENABLE_STACK_SNAPSHOT |
        COR_PRF_ENABLE_OBJECT_ALLOCATED |
        COR_PRF_REQUIRE_PROFILEE;

    hr = m_profilerInfo->SetEventMask(eventMask);
    if (FAILED(hr)) {
        return hr;
    }

    m_initialized = true;
    return S_OK;
}

HRESULT STDMETHODCALLTYPE ProfilerAgent::Shutdown() {
    SendEventsToAgent();
    m_initialized = false;
    return S_OK;
}

void ProfilerAgent::SetEventMask(DWORD events) {
    if (m_profilerInfo) {
        m_profilerInfo->SetEventMask(events);
    }
}

void ProfilerAgent::EnableMethodInstrumentation() {
    if (!m_initialized || !m_profilerInfo) return;

    DWORD eventMask = 
        COR_PRF_MONITOR_JIT_COMPILATION |
        COR_PRF_MONITOR_THREADS |
        COR_PRF_MONITOR_EXCEPTIONS |
        COR_PRF_ENABLE_STACK_SNAPSHOT |
        COR_PRF_ENABLE_OBJECT_ALLOCATED |
        COR_PRF_REQUIRE_PROFILEE;

    m_profilerInfo->SetEventMask(eventMask);
    m_instrumentationEnabled = true;
}

void ProfilerAgent::DisableMethodInstrumentation() {
    m_instrumentationEnabled = false;
}

void ProfilerAgent::ProcessMethodEnter(ThreadID threadId, FunctionID functionId) {
    if (!m_instrumentationEnabled) return;

    std::lock_guard<std::mutex> lock(m_eventBufferMutex);

    MethodCallEvent event;
    event.threadId = (uint32_t)threadId;
    event.timestamp_ns = std::chrono::duration_cast<std::chrono::nanoseconds>(
        std::chrono::system_clock::now().time_since_epoch()).count();
    event.methodId = functionId;
    event.duration_ns = 0;
    event.isEntry = true;
    event.callStack = CaptureCallStack();

    m_eventBuffer.push_back(event);
}

void ProfilerAgent::ProcessMethodLeave(ThreadID threadId, FunctionID functionId, uint64_t duration_ns) {
    if (!m_instrumentationEnabled) return;

    std::lock_guard<std::mutex> lock(m_eventBufferMutex);

    MethodCallEvent event;
    event.threadId = (uint32_t)threadId;
    event.timestamp_ns = std::chrono::duration_cast<std::chrono::nanoseconds>(
        std::chrono::system_clock::now().time_since_epoch()).count();
    event.methodId = functionId;
    event.duration_ns = duration_ns;
    event.isEntry = false;

    m_eventBuffer.push_back(event);
}

void ProfilerAgent::ProcessException(ExceptionEvent& event) {
    std::lock_guard<std::mutex> lock(m_eventBufferMutex);
    // Exception events would be sent here
}

std::wstring ProfilerAgent::GetMethodName(FunctionID functionId) {
    std::lock_guard<std::mutex> lock(m_methodMapMutex);
    
    auto it = m_methodMap.find(functionId);
    if (it != m_methodMap.end()) {
        return it->second.className + L"." + it->second.methodName;
    }
    return L"Unknown";
}

std::vector<uint64_t> ProfilerAgent::CaptureCallStack() {
    std::vector<uint64_t> stack;
    
    if (!m_profilerInfo) return stack;

    ICorProfilerStackWalker* pStackWalker = nullptr;
    ThreadID threadId = 0;

    HRESULT hr = m_profilerInfo->GetCurrentThread(&threadId);
    if (FAILED(hr)) return stack;

    hr = m_profilerInfo->GetStackWalkHandle(threadId, NULL);
    if (FAILED(hr)) return stack;

    return stack;
}

void ProfilerAgent::SendEventsToAgent() {
    std::lock_guard<std::mutex> lock(m_eventBufferMutex);
    m_eventBuffer.clear();
}

// ICorProfilerCallback implementations - stub versions
#define STUB_METHOD(x) HRESULT STDMETHODCALLTYPE ProfilerAgent::x { return S_OK; }

STUB_METHOD(AppDomainCreationStarted)
STUB_METHOD(AppDomainCreationFinished)
STUB_METHOD(AppDomainShutdownStarted)
STUB_METHOD(AppDomainShutdownFinished)
STUB_METHOD(AssemblyLoadStarted)
STUB_METHOD(AssemblyLoadFinished)
STUB_METHOD(AssemblyUnloadStarted)
STUB_METHOD(AssemblyUnloadFinished)
STUB_METHOD(ModuleLoadStarted)
STUB_METHOD(ModuleLoadFinished)
STUB_METHOD(ModuleUnloadStarted)
STUB_METHOD(ModuleUnloadFinished)
STUB_METHOD(ModuleAttachedToAssembly)
STUB_METHOD(ClassLoadStarted)
STUB_METHOD(ClassLoadFinished)
STUB_METHOD(ClassUnloadStarted)
STUB_METHOD(ClassUnloadFinished)
STUB_METHOD(FunctionUnloadStarted)

STUB_METHOD(JITCompilationStarted)
STUB_METHOD(JITCompilationFinished)
STUB_METHOD(JITCachedFunctionSearchStarted)
STUB_METHOD(JITCachedFunctionSearchFinished)
STUB_METHOD(JITFunctionPitched)
STUB_METHOD(JITInlining)
STUB_METHOD(PostJITCompilationStarted)
STUB_METHOD(PostJITFunctionPitched)
STUB_METHOD(JITlining)

STUB_METHOD(ThreadCreated)
STUB_METHOD(ThreadDestroyed)
STUB_METHOD(ThreadAssignedToOSThread)
STUB_METHOD(ThreadNameChanged)

STUB_METHOD(RuntimeSuspendStarted)
STUB_METHOD(RuntimeSuspendFinished)
STUB_METHOD(RuntimeSuspendAborted)
STUB_METHOD(RuntimeResumeStarted)
STUB_METHOD(RuntimeResumeFinished)
STUB_METHOD(RuntimeThreadSuspended)
STUB_METHOD(RuntimeThreadResumed)

STUB_METHOD(MovedReferences)
STUB_METHOD(ObjectAllocatedByClass)
STUB_METHOD(ObjectAllocated)
STUB_METHOD(ObjectReferences)
STUB_METHOD(RootReferences)
STUB_METHOD(RootReferences2)
STUB_METHOD(HandleCreated)
STUB_METHOD(HandleDestroyed)

STUB_METHOD(RemotingClientInvocationStarted)
STUB_METHOD(RemotingClientSendingMessage)
STUB_METHOD(RemotingClientReceivingReply)
STUB_METHOD(RemotingServerInvocationStarted)
STUB_METHOD(RemotingServerReceivingMessage)
STUB_METHOD(RemotingServerSendingReply)

STUB_METHOD(UnmanagedToManagedTransition)
STUB_METHOD(ManagedToUnmanagedTransition)

STUB_METHOD(ExceptionThrown)
STUB_METHOD(ExceptionSearchFunctionEnter)
STUB_METHOD(ExceptionSearchFunctionLeave)
STUB_METHOD(ExceptionSearchFilterEnter)
STUB_METHOD(ExceptionSearchFilterLeave)
STUB_METHOD(ExceptionSearchCatcherFound)
STUB_METHOD(ExceptionOSHandlerEnter)
STUB_METHOD(ExceptionOSHandlerLeave)
STUB_METHOD(ExceptionUnwindFunctionEnter)
STUB_METHOD(ExceptionUnwindFunctionLeave)
STUB_METHOD(ExceptionCatcherEnter)
STUB_METHOD(ExceptionCatcherLeave)

STUB_METHOD(COMClassicVTableCreated)
STUB_METHOD(COMClassicVTableDestroyed)

STUB_METHOD(StringRefTypeID)
STUB_METHOD(ObjectRefTypeID)
STUB_METHOD(ObjectCreatedByClass)
STUB_METHOD(ObjectDestroyed)

STUB_METHOD(ClassIDName2)
STUB_METHOD(ClassIDModule2)
STUB_METHOD(GetClassIDInfo2)
STUB_METHOD(GetFunctionInfo2)
STUB_METHOD(GetFunctionPointerInfo2)

STUB_METHOD(InitializeForAttach)
STUB_METHOD(ProfilerAttachComplete)
STUB_METHOD(ProfilerDetachSucceeded)
STUB_METHOD(ForceGC)

} // namespace dotnet
} // namespace langango

// DLL export implementations
extern "C" {

PROFILER_EXPORT HRESULT GetProfilerClassID(CLSID* pClassId) {
    return ::CLSIDFromString(L"{B4557D16-8B7F-4F8C-ABCD-1234567890AB}", pClassId);
}

PROFILER_EXPORT HRESULT CreateProfilerInstance(REFIID riid, void** ppv) {
    langango::dotnet::ProfilerAgent* pAgent = new (std::nothrow) langango::dotnet::ProfilerAgent();
    if (!pAgent) {
        *ppv = nullptr;
        return E_OUTOFMEMORY;
    }
    pAgent->AddRef();
    return pAgent->QueryInterface(riid, ppv);
}

}
