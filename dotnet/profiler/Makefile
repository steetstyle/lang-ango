# Makefile for LangAngo .NET Profiler
# Compiles to a shared library (.so)

CXX := g++
CXXFLAGS := -std=c++17 -fPIC -O2 -g -Wall
CXXFLAGS += -DFEATURE_PAL -DPLATFORM_UNIX -DHOST_64BIT -DBIT64 -DDLL_EXPORT
CXXFLAGS += -DHOST_AMD64 -D_TARGET_AMD64_ -Dx86_64

# Directories
PROFILER_DIR := $(shell pwd)
EXTERNAL_DIR := $(PROFILER_DIR)/external/runtime/src
INC_DIR := $(EXTERNAL_DIR)/coreclr/inc
PAL_PREBUILT := $(EXTERNAL_DIR)/coreclr/pal/prebuilt/inc
PAL_INC := $(EXTERNAL_DIR)/coreclr/pal/inc
PAL_RT := $(EXTERNAL_DIR)/coreclr/pal/inc/rt
PAL_MINIPAL := $(EXTERNAL_DIR)/coreclr/pal/inc/minipal
NATIVE_MINIPAL := $(EXTERNAL_DIR)/native/minipal
NATIVE := $(EXTERNAL_DIR)/native

CXXFLAGS += -I$(INC_DIR) -I$(PAL_PREBUILT) -I$(PAL_INC) -I$(PAL_RT) -I$(PAL_MINIPAL) -I$(NATIVE_MINIPAL) -I$(NATIVE) -I$(PROFILER_DIR)

LDFLAGS := -shared -pthread -ldl

TARGET := liblangango_profiler.so
SOURCES := profiler.cpp
OBJECTS := $(SOURCES:.cpp=.o)

.PHONY: all clean

all: $(TARGET)

$(TARGET): $(OBJECTS)
	$(CXX) $(LDFLAGS) -o $@ $^

%.o: %.cpp profiler.h
	$(CXX) $(CXXFLAGS) -c -o $@ $<

clean:
	rm -f $(OBJECTS) $(TARGET)

run: $(TARGET)
	@echo "Built: $(TARGET)"
	@echo "To use: set COR_PROFILER_PATH=$(PROFILER_DIR)/$(TARGET)"
