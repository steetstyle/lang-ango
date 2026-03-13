# Makefile for LangAngo Bridge (C++ library for P/Invoke)

CXX := g++
CXXFLAGS := -std=c++17 -fPIC -O2 -g -Wall -shared
LDFLAGS := -pthread

TARGET := liblangango_bridge.so
SOURCES := bridge.cpp

.PHONY: all clean install

all: $(TARGET)

$(TARGET): $(SOURCES)
	$(CXX) $(CXXFLAGS) $(LDFLAGS) -o $@ $<

clean:
	rm -f $(TARGET)

install: $(TARGET)
	cp $(TARGET) /usr/local/lib/
	ldconfig
