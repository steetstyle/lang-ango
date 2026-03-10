/*
 * Lang-Ango .NET Profiler Unit Tests
 * GoogleTest based unit tests for the profiler
 */

#include <gtest/gtest.h>
#include <gmock/gmock.h>
#include <cstdint>
#include <memory>
#include <string>
#include <vector>

#include "profiler.h"

using namespace langango::dotnet;

class MockICorProfilerInfo {
public:
    MOCK_METHOD3(GetClassIDInfo, HRESULT(ClassID classId, ModuleID* pModuleId, mdTypeDef* pTypeDefToken));
    MOCK_METHOD5(GetFunctionInfo, HRESULT(FunctionID functionId, AppDomainID* pAppDomainId, ModuleID* pModuleId, mdToken* pToken, ClassID* pClassId));
    MOCK_METHOD2(SetEventMask, HRESULT(DWORD dwEventMask));
    MOCK_METHOD2(GetCurrentThread, HRESULT(ThreadID* pThreadId));
};

class ProfilerAgentTest : public ::testing::Test {
protected:
    void SetUp() override {
        profiler_ = std::make_unique<ProfilerAgent>();
    }

    std::unique_ptr<ProfilerAgent> profiler_;
};

TEST_F(ProfilerAgentTest, InitializesCorrectly) {
    EXPECT_NE(profiler_, nullptr);
}

TEST_F(ProfilerAgentTest, QueryInterfaceSucceedsForIUnknown) {
    IUnknown* pUnk = nullptr;
    HRESULT hr = profiler_->QueryInterface(IID_IUnknown, (void**)&pUnk);
    EXPECT_EQ(hr, S_OK);
    EXPECT_NE(pUnk, nullptr);
    if (pUnk) {
        pUnk->Release();
    }
}

TEST_F(ProfilerAgentTest, QueryInterfaceSucceedsForICorProfilerCallback) {
    ICorProfilerCallback* pCallback = nullptr;
    HRESULT hr = profiler_->QueryInterface(IID_ICorProfilerCallback, (void**)&pCallback);
    EXPECT_EQ(hr, S_OK);
    EXPECT_NE(pCallback, nullptr);
    if (pCallback) {
        pCallback->Release();
    }
}

TEST_F(ProfilerAgentTest, AddRefIncrementsRefCount) {
    ULONG initialCount = profiler_->AddRef();
    EXPECT_GT(initialCount, 0);
}

TEST_F(ProfilerAgentTest, ReleaseDecrementsRefCount) {
    profiler_->AddRef();
    ULONG count = profiler_->Release();
    EXPECT_EQ(count, 0);
}

TEST_F(ProfilerAgentTest, SetEventMaskSucceeds) {
    DWORD eventMask = COR_PRF_MONITOR_JIT_COMPILATION | COR_PRF_MONITOR_THREADS;
    profiler_->SetEventMask(eventMask);
}

TEST_F(ProfilerAgentTest, EnableMethodInstrumentationSucceeds) {
    EXPECT_NO_THROW(profiler_->EnableMethodInstrumentation());
}

TEST_F(ProfilerAgentTest, DisableMethodInstrumentationSucceeds) {
    EXPECT_NO_THROW(profiler_->DisableMethodInstrumentation());
}

TEST_F(ProfilerAgentTest, GetMethodNameReturnsUnknownForInvalidFunctionId) {
    std::wstring methodName = profiler_->GetMethodName(0);
    EXPECT_EQ(methodName, L"Unknown");
}

TEST_F(ProfilerAgentTest, CaptureCallStackReturnsVector) {
    std::vector<uint64_t> stack = profiler_->CaptureCallStack();
    EXPECT_NE(stack.data(), nullptr);
}

TEST(MethodInfoTest, MethodInfoStoresDataCorrectly) {
    MethodInfo info;
    info.className = L"TestClass";
    info.methodName = L"TestMethod";
    info.methodToken = 0x06000001;

    EXPECT_EQ(info.className, L"TestClass");
    EXPECT_EQ(info.methodName, L"TestMethod");
    EXPECT_EQ(info.methodToken, 0x06000001);
}

TEST(MethodCallEventTest, MethodCallEventStoresDataCorrectly) {
    MethodCallEvent event;
    event.threadId = 1234;
    event.timestamp_ns = 1234567890;
    event.methodId = 5678;
    event.duration_ns = 1000000;
    event.isEntry = true;

    EXPECT_EQ(event.threadId, 1234);
    EXPECT_EQ(event.timestamp_ns, 1234567890);
    EXPECT_EQ(event.methodId, 5678);
    EXPECT_EQ(event.duration_ns, 1000000);
    EXPECT_TRUE(event.isEntry);
}

TEST(ExceptionEventTest, ExceptionEventStoresDataCorrectly) {
    ExceptionEvent event;
    event.threadId = 5678;
    event.timestamp_ns = 9876543210;
    event.exceptionType = L"SbmException";
    event.message = L"Header elemanının formatı hatalıdır.";

    EXPECT_EQ(event.threadId, 5678);
    EXPECT_EQ(event.timestamp_ns, 9876543210);
    EXPECT_EQ(event.exceptionType, L"SbmException");
    EXPECT_EQ(event.message, L"Header elemanının formatı hatalıdır.");
}

TEST(ExceptionEventTest, StackTraceStoresMultipleFrames) {
    ExceptionEvent event;
    event.stackTrace.push_back(L"at SDK.SBM.Services.EgmServiceRest.TescilSorgu()");
    event.stackTrace.push_back(L"at Infrastructure.Common.Exceptions.SbmException.Throw()");
    event.stackTrace.push_back(L"at System.Threading.Tasks.AwaitTaskContinuation.Run()");

    EXPECT_EQ(event.stackTrace.size(), 3);
    EXPECT_EQ(event.stackTrace[0], L"at SDK.SBM.Services.EgmServiceRest.TescilSorgu()");
    EXPECT_EQ(event.stackTrace[2], L"at System.Threading.Tasks.AwaitTaskContinuation.Run()");
}

int main(int argc, char **argv) {
    ::testing::InitGoogleTest(&argc, argv);
    return RUN_ALL_TESTS();
}
