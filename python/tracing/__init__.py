"""
Lang-Ango Python Tracing Module

Provides function-level tracing for Python applications using
sys.settrace and AST instrumentation.
"""

import sys
import os
import json
import time
import threading
import traceback
import functools
from typing import Dict, List, Optional, Callable, Any
from contextlib import contextmanager
from dataclasses import dataclass, field
from collections import defaultdict


@dataclass
class MethodCallEvent:
    """Represents a Python method call event"""

    timestamp_ns: int
    thread_id: int
    method_name: str
    class_name: str
    file_path: str
    line_number: int
    duration_ns: int = 0
    is_entry: bool = True
    stack_depth: int = 0
    arguments: Dict[str, Any] = field(default_factory=dict)
    return_value: Any = None


@dataclass
class ExceptionEvent:
    """Represents a Python exception"""

    timestamp_ns: int
    thread_id: int
    exception_type: str
    exception_value: str
    stack_trace: List[str] = field(default_factory=list)
    method_name: str = ""
    file_path: str = ""
    line_number: int = 0


class PythonTracer:
    """
    Main Python tracing class that instruments Python code
    to capture method calls, timing, and exceptions.
    """

    def __init__(self, agent_endpoint: str = "localhost:4317"):
        self.agent_endpoint = agent_endpoint
        self.enabled = False
        self._local = threading.local()
        self._call_stack: List[MethodCallEvent] = []
        self._event_buffer: List[MethodCallEvent] = []
        self._exception_buffer: List[ExceptionEvent] = []
        self._buffer_lock = threading.Lock()
        self._method_counts = defaultdict(int)
        self._sampling_rate = 1.0

        self._original_tracer: Optional[Callable] = None
        self._wrapped_functions: Dict[int, Callable] = {}

        self._ignored_modules = {
            "lang_ango",
            "site",
            "encodings",
            "codecs",
            "abc",
            "collections",
            "functools",
            "itertools",
        }
        self._ignored_files = set()

    def start(self):
        """Enable tracing"""
        if self.enabled:
            return

        self.enabled = True
        self._original_tracer = sys.gettrace()
        sys.settrace(self._tracer)

        self._flush_thread = threading.Thread(target=self._flush_events, daemon=True)
        self._flush_thread.start()

    def stop(self):
        """Disable tracing"""
        if not self.enabled:
            return

        self.enabled = False
        sys.settrace(self._original_tracer)

        with self._buffer_lock:
            self._flush_buffer()

    def instrument_function(self, func: Callable) -> Callable:
        """Decorator to instrument a specific function"""

        @functools.wraps(func)
        def wrapper(*args, **kwargs):
            if not self.enabled:
                return func(*args, **kwargs)

            event = MethodCallEvent(
                timestamp_ns=int(time.time() * 1e9),
                thread_id=threading.get_ident(),
                method_name=func.__name__,
                class_name=getattr(func, "__class__", None).__name__
                if hasattr(func, "__class__") and func.__class__ is not None
                else "",
                file_path=func.__code__.co_filename,
                line_number=func.__code__.co_firstlineno,
                is_entry=True,
                stack_depth=len(self._call_stack),
                arguments=self._serialize_args(args, kwargs),
            )

            self._record_event(event)

            try:
                start_time = time.perf_counter_ns()
                result = func(*args, **kwargs)
                duration = time.perf_counter_ns() - start_time

                event_exit = MethodCallEvent(
                    timestamp_ns=int(time.time() * 1e9),
                    thread_id=threading.get_ident(),
                    method_name=func.__name__,
                    class_name=getattr(func, "__class__", None).__name__
                    if hasattr(func, "__class__") and func.__class__ is not None
                    else "",
                    file_path=func.__code__.co_filename,
                    line_number=func.__code__.co_firstlineno,
                    is_entry=False,
                    duration_ns=duration,
                    return_value=self._serialize_value(result),
                )

                self._record_event(event_exit)
                return result

            except Exception as e:
                exc_event = ExceptionEvent(
                    timestamp_ns=int(time.time() * 1e9),
                    thread_id=threading.get_ident(),
                    exception_type=type(e).__name__,
                    exception_value=str(e),
                    stack_trace=traceback.format_exc().split("\n"),
                    method_name=func.__name__,
                    file_path=func.__code__.co_filename,
                    line_number=func.__code__.co_firstlineno,
                )
                self._record_exception(exc_event)
                raise

        return wrapper

    @contextmanager
    def instrument_block(self, name: str):
        """Context manager for instrumenting code blocks"""
        if not self.enabled:
            yield
            return

        event = MethodCallEvent(
            timestamp_ns=int(time.time() * 1e9),
            thread_id=threading.get_ident(),
            method_name=name,
            class_name="",
            file_path="",
            line_number=0,
            is_entry=True,
            stack_depth=len(self._call_stack),
        )

        self._record_event(event)

        try:
            yield
        except Exception as e:
            exc_event = ExceptionEvent(
                timestamp_ns=int(time.time() * 1e9),
                thread_id=threading.get_ident(),
                exception_type=type(e).__name__,
                exception_value=str(e),
                stack_trace=traceback.format_exc().split("\n"),
                method_name=name,
            )
            self._record_exception(exc_event)
            raise
        finally:
            duration = 0
            event_exit = MethodCallEvent(
                timestamp_ns=int(time.time() * 1e9),
                thread_id=threading.get_ident(),
                method_name=name,
                class_name="",
                file_path="",
                line_number=0,
                is_entry=False,
                duration_ns=duration,
            )
            self._record_event(event_exit)

    def _tracer(self, frame, event, arg):
        """System tracer callback"""
        if not self.enabled:
            return self._original_tracer

        if event == "call":
            self._handle_call(frame)
        elif event == "return":
            self._handle_return(frame, arg)
        elif event == "exception":
            self._handle_exception(frame, arg)

        return self._tracer

    def _handle_call(self, frame):
        """Handle function call"""
        code = frame.f_code
        filename = code.co_filename

        if self._should_ignore(filename):
            return

        event = MethodCallEvent(
            timestamp_ns=int(time.time() * 1e9),
            thread_id=threading.get_ident(),
            method_name=code.co_name,
            class_name=frame.f_locals.get("__class__", "").__name__
            if "__class__" in frame.f_locals
            else "",
            file_path=filename,
            line_number=code.co_firstlineno,
            is_entry=True,
            stack_depth=len(self._call_stack),
        )

        self._record_event(event)
        self._call_stack.append(event)

    def _handle_return(self, frame, arg):
        """Handle function return"""
        if not self._call_stack:
            return

        code = frame.f_code
        filename = code.co_filename

        if self._should_ignore(filename):
            return

        entry = self._call_stack.pop()
        duration = int(time.time() * 1e9) - entry.timestamp_ns

        event = MethodCallEvent(
            timestamp_ns=int(time.time() * 1e9),
            thread_id=threading.get_ident(),
            method_name=code.co_name,
            class_name=entry.class_name,
            file_path=filename,
            line_number=code.co_firstlineno,
            is_entry=False,
            duration_ns=duration,
            return_value=self._serialize_value(arg),
        )

        self._record_event(event)

    def _handle_exception(self, frame, arg):
        """Handle exception"""
        exc_type, exc_value, exc_tb = arg

        event = ExceptionEvent(
            timestamp_ns=int(time.time() * 1e9),
            thread_id=threading.get_ident(),
            exception_type=exc_type.__name__ if exc_type else "",
            exception_value=str(exc_value) if exc_value else "",
            stack_trace=traceback.format_exception(exc_type, exc_value, exc_tb),
            method_name=frame.f_code.co_name,
            file_path=frame.f_code.co_filename,
            line_number=frame.f_lineno,
        )

        self._record_exception(event)

    def _record_event(self, event: MethodCallEvent):
        """Record a method call event"""
        if event.is_entry or event.duration_ns > 0:
            self._method_counts[event.method_name] += 1

        with self._buffer_lock:
            self._event_buffer.append(event)

            if len(self._event_buffer) >= 1000:
                self._flush_buffer()

    def _record_exception(self, event: ExceptionEvent):
        """Record an exception event"""
        with self._buffer_lock:
            self._exception_buffer.append(event)

    def _flush_buffer(self):
        """Flush event buffer to agent"""
        if not self._event_buffer and not self._exception_buffer:
            return

        events = self._event_buffer
        exceptions = self._exception_buffer

        self._event_buffer = []
        self._exception_buffer = []

        payload = {
            "method_events": [self._serialize_event(e) for e in events],
            "exception_events": [self._serialize_exception(e) for e in exceptions],
        }

        self._send_to_agent(payload)

    def _flush_events(self):
        """Background thread to periodically flush events"""
        while self.enabled:
            time.sleep(1.0)
            with self._buffer_lock:
                self._flush_buffer()

    def _send_to_agent(self, payload: Dict):
        """Send events to the main agent"""
        try:
            import urllib.request
            import urllib.error

            data = json.dumps(payload).encode("utf-8")
            req = urllib.request.Request(
                f"http://{self.agent_endpoint}/v1/traces/python",
                data=data,
                headers={"Content-Type": "application/json"},
            )
            urllib.request.urlopen(req, timeout=5)
        except Exception:
            pass

    def _should_ignore(self, filename: str) -> bool:
        """Check if file should be ignored"""
        if not filename:
            return True

        basename = os.path.basename(filename)

        if basename in self._ignored_files:
            return True

        for mod in self._ignored_modules:
            if mod in filename:
                return True

        return False

    def _serialize_args(self, args, kwargs) -> Dict:
        """Serialize function arguments"""
        result = {}
        for i, arg in enumerate(args):
            result[f"arg_{i}"] = self._serialize_value(arg)
        for k, v in kwargs.items():
            result[k] = self._serialize_value(v)
        return result

    def _serialize_value(self, value: Any) -> str:
        """Serialize a value to string"""
        if value is None:
            return "None"
        if isinstance(value, (int, float, bool, str)):
            return str(value)
        if isinstance(value, bytes):
            return f"<bytes: {len(value)}>"
        if hasattr(value, "__class__"):
            return f"<{value.__class__.__name__}>"
        return str(type(value))

    def _serialize_event(self, event: MethodCallEvent) -> Dict:
        """Serialize event to dict"""
        return {
            "timestamp_ns": event.timestamp_ns,
            "thread_id": event.thread_id,
            "method_name": event.method_name,
            "class_name": event.class_name,
            "file_path": event.file_path,
            "line_number": event.line_number,
            "duration_ns": event.duration_ns,
            "is_entry": event.is_entry,
            "stack_depth": event.stack_depth,
            "arguments": event.arguments,
            "return_value": event.return_value,
        }

    def _serialize_exception(self, event: ExceptionEvent) -> Dict:
        """Serialize exception to dict"""
        return {
            "timestamp_ns": event.timestamp_ns,
            "thread_id": event.thread_id,
            "exception_type": event.exception_type,
            "exception_value": event.exception_value,
            "stack_trace": event.stack_trace,
            "method_name": event.method_name,
            "file_path": event.file_path,
            "line_number": event.line_number,
        }


_tracer_instance: Optional[PythonTracer] = None


def get_tracer() -> PythonTracer:
    """Get or create the global tracer instance"""
    global _tracer_instance
    if _tracer_instance is None:
        _tracer_instance = PythonTracer()
    return _tracer_instance


def instrument(agent_endpoint: str = "localhost:4317"):
    """Start Python instrumentation"""
    tracer = get_tracer()
    tracer.start()
    return tracer


def stop():
    """Stop Python instrumentation"""
    tracer = get_tracer()
    tracer.stop()


def trace_function(func: Callable) -> Callable:
    """Decorator to trace a specific function"""
    return get_tracer().instrument_function(func)
