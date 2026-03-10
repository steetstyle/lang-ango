/*
 * Lang-Ango .NET Profiler Agent
 * Implements ICorProfilerCallback for method instrumentation
 * 
 * This is a C++ shared library that gets injected into .NET processes
 * to capture method entry/exit with full stack traces and timing.
 */

#ifndef LANG_ANGO_PROFILER_H
#define LANG_ANGO_PROFILER_H

#include <corprof.h>
#include <corhdr.h>
#include <atomic>
#include <chrono>
#include <map>
#include <mutex>
#include <string>
#include <vector>

#ifdef _WIN32
#define PROFILER_EXPORT __declspec(dllexport)
#else
#define PROFILER_EXPORT __attribute__((visibility("default")))
#endif

namespace langango {
namespace dotnet {

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
    HRESULT STDMETHODCALLTYPE COMClassicVTableDestroyed(ClassID classId, REFGUID implementedIID, void* pVTable) override;
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

    // Custom methods for event handling
    void SetEventMask(DWORD events);
    void EnableMethodInstrumentation();
    void DisableMethodInstrumentation();

private:
    std::atomic<ULONG> m_refCount;
    ICorProfilerInfo* m_profilerInfo;
    bool m_initialized;
    bool m_instrumentationEnabled;
    
    std::mutex m_methodMapMutex;
    std::map<FunctionID, MethodInfo> m_methodMap;
    
    std::mutex m_eventBufferMutex;
    std::vector<MethodCallEvent> m_eventBuffer;
    
    std::atomic<uint64_t> m_eventSequence;
    
    void ProcessMethodEnter(ThreadID threadId, FunctionID functionId);
    void ProcessMethodLeave(ThreadID threadId, FunctionID functionId, uint64_t duration_ns);
    void ProcessException(ExceptionEvent& event);
    std::wstring GetMethodName(FunctionID functionId);
    std::vector<uint64_t> CaptureCallStack();
    void SendEventsToAgent();
};

} // namespace dotnet
} // namespace langango

// DLL entry points
extern "C" {
    PROFILER_EXPORT HRESULT GetProfilerClassID(CLSID* pClassId);
    PROFILER_EXPORT HRESULT CreateProfilerInstance(REFIID riid, void** ppv);
}

#endif // LANG_ANGO_PROFILER_H
