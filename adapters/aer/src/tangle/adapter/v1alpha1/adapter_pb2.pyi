import datetime

from google.protobuf import timestamp_pb2 as _timestamp_pb2
from google.protobuf import duration_pb2 as _duration_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class ListTargetsRequest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class ListTargetsResponse(_message.Message):
    __slots__ = ("targets",)
    TARGETS_FIELD_NUMBER: _ClassVar[int]
    targets: _containers.RepeatedCompositeFieldContainer[TargetInfo]
    def __init__(self, targets: _Optional[_Iterable[_Union[TargetInfo, _Mapping]]] = ...) -> None: ...

class TargetRef(_message.Message):
    __slots__ = ("target_id",)
    TARGET_ID_FIELD_NUMBER: _ClassVar[int]
    target_id: str
    def __init__(self, target_id: _Optional[str] = ...) -> None: ...

class TargetInfo(_message.Message):
    __slots__ = ("target_id", "display_name", "vendor", "modality", "simulator")
    TARGET_ID_FIELD_NUMBER: _ClassVar[int]
    DISPLAY_NAME_FIELD_NUMBER: _ClassVar[int]
    VENDOR_FIELD_NUMBER: _ClassVar[int]
    MODALITY_FIELD_NUMBER: _ClassVar[int]
    SIMULATOR_FIELD_NUMBER: _ClassVar[int]
    target_id: str
    display_name: str
    vendor: str
    modality: str
    simulator: bool
    def __init__(self, target_id: _Optional[str] = ..., display_name: _Optional[str] = ..., vendor: _Optional[str] = ..., modality: _Optional[str] = ..., simulator: _Optional[bool] = ...) -> None: ...

class Capabilities(_message.Message):
    __slots__ = ("target", "num_qubits", "coupling_map", "native_gates", "program_formats", "max_shots", "sessions", "cancellation", "billing_units", "coupling_class", "vendor_extensions")
    class VendorExtensionsEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    TARGET_FIELD_NUMBER: _ClassVar[int]
    NUM_QUBITS_FIELD_NUMBER: _ClassVar[int]
    COUPLING_MAP_FIELD_NUMBER: _ClassVar[int]
    NATIVE_GATES_FIELD_NUMBER: _ClassVar[int]
    PROGRAM_FORMATS_FIELD_NUMBER: _ClassVar[int]
    MAX_SHOTS_FIELD_NUMBER: _ClassVar[int]
    SESSIONS_FIELD_NUMBER: _ClassVar[int]
    CANCELLATION_FIELD_NUMBER: _ClassVar[int]
    BILLING_UNITS_FIELD_NUMBER: _ClassVar[int]
    COUPLING_CLASS_FIELD_NUMBER: _ClassVar[int]
    VENDOR_EXTENSIONS_FIELD_NUMBER: _ClassVar[int]
    target: TargetInfo
    num_qubits: int
    coupling_map: _containers.RepeatedCompositeFieldContainer[CouplingEdge]
    native_gates: _containers.RepeatedScalarFieldContainer[str]
    program_formats: _containers.RepeatedScalarFieldContainer[str]
    max_shots: int
    sessions: bool
    cancellation: bool
    billing_units: _containers.RepeatedScalarFieldContainer[str]
    coupling_class: str
    vendor_extensions: _containers.ScalarMap[str, str]
    def __init__(self, target: _Optional[_Union[TargetInfo, _Mapping]] = ..., num_qubits: _Optional[int] = ..., coupling_map: _Optional[_Iterable[_Union[CouplingEdge, _Mapping]]] = ..., native_gates: _Optional[_Iterable[str]] = ..., program_formats: _Optional[_Iterable[str]] = ..., max_shots: _Optional[int] = ..., sessions: _Optional[bool] = ..., cancellation: _Optional[bool] = ..., billing_units: _Optional[_Iterable[str]] = ..., coupling_class: _Optional[str] = ..., vendor_extensions: _Optional[_Mapping[str, str]] = ...) -> None: ...

class CouplingEdge(_message.Message):
    __slots__ = ("a", "b")
    A_FIELD_NUMBER: _ClassVar[int]
    B_FIELD_NUMBER: _ClassVar[int]
    a: int
    b: int
    def __init__(self, a: _Optional[int] = ..., b: _Optional[int] = ...) -> None: ...

class DeviceState(_message.Message):
    __slots__ = ("target", "status", "queue_depth", "estimated_wait", "unknown_queue", "calibration", "maintenance", "observed_at")
    class Status(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
        __slots__ = ()
        STATUS_UNSPECIFIED: _ClassVar[DeviceState.Status]
        ONLINE: _ClassVar[DeviceState.Status]
        CALIBRATING: _ClassVar[DeviceState.Status]
        MAINTENANCE: _ClassVar[DeviceState.Status]
        DEGRADED: _ClassVar[DeviceState.Status]
        OFFLINE: _ClassVar[DeviceState.Status]
    STATUS_UNSPECIFIED: DeviceState.Status
    ONLINE: DeviceState.Status
    CALIBRATING: DeviceState.Status
    MAINTENANCE: DeviceState.Status
    DEGRADED: DeviceState.Status
    OFFLINE: DeviceState.Status
    TARGET_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    QUEUE_DEPTH_FIELD_NUMBER: _ClassVar[int]
    ESTIMATED_WAIT_FIELD_NUMBER: _ClassVar[int]
    UNKNOWN_QUEUE_FIELD_NUMBER: _ClassVar[int]
    CALIBRATION_FIELD_NUMBER: _ClassVar[int]
    MAINTENANCE_FIELD_NUMBER: _ClassVar[int]
    OBSERVED_AT_FIELD_NUMBER: _ClassVar[int]
    target: TargetRef
    status: DeviceState.Status
    queue_depth: int
    estimated_wait: _duration_pb2.Duration
    unknown_queue: bool
    calibration: CalibrationSnapshot
    maintenance: _containers.RepeatedCompositeFieldContainer[MaintenanceWindow]
    observed_at: _timestamp_pb2.Timestamp
    def __init__(self, target: _Optional[_Union[TargetRef, _Mapping]] = ..., status: _Optional[_Union[DeviceState.Status, str]] = ..., queue_depth: _Optional[int] = ..., estimated_wait: _Optional[_Union[datetime.timedelta, _duration_pb2.Duration, _Mapping]] = ..., unknown_queue: _Optional[bool] = ..., calibration: _Optional[_Union[CalibrationSnapshot, _Mapping]] = ..., maintenance: _Optional[_Iterable[_Union[MaintenanceWindow, _Mapping]]] = ..., observed_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class CalibrationSnapshot(_message.Message):
    __slots__ = ("snapshot_id", "measured_at", "source", "metrics")
    SNAPSHOT_ID_FIELD_NUMBER: _ClassVar[int]
    MEASURED_AT_FIELD_NUMBER: _ClassVar[int]
    SOURCE_FIELD_NUMBER: _ClassVar[int]
    METRICS_FIELD_NUMBER: _ClassVar[int]
    snapshot_id: str
    measured_at: _timestamp_pb2.Timestamp
    source: str
    metrics: _containers.RepeatedCompositeFieldContainer[Metric]
    def __init__(self, snapshot_id: _Optional[str] = ..., measured_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., source: _Optional[str] = ..., metrics: _Optional[_Iterable[_Union[Metric, _Mapping]]] = ...) -> None: ...

class Metric(_message.Message):
    __slots__ = ("name", "value", "unit", "modality", "methodology", "confidence", "qubits")
    NAME_FIELD_NUMBER: _ClassVar[int]
    VALUE_FIELD_NUMBER: _ClassVar[int]
    UNIT_FIELD_NUMBER: _ClassVar[int]
    MODALITY_FIELD_NUMBER: _ClassVar[int]
    METHODOLOGY_FIELD_NUMBER: _ClassVar[int]
    CONFIDENCE_FIELD_NUMBER: _ClassVar[int]
    QUBITS_FIELD_NUMBER: _ClassVar[int]
    name: str
    value: float
    unit: str
    modality: str
    methodology: str
    confidence: float
    qubits: _containers.RepeatedScalarFieldContainer[int]
    def __init__(self, name: _Optional[str] = ..., value: _Optional[float] = ..., unit: _Optional[str] = ..., modality: _Optional[str] = ..., methodology: _Optional[str] = ..., confidence: _Optional[float] = ..., qubits: _Optional[_Iterable[int]] = ...) -> None: ...

class MaintenanceWindow(_message.Message):
    __slots__ = ("start", "end", "reason")
    START_FIELD_NUMBER: _ClassVar[int]
    END_FIELD_NUMBER: _ClassVar[int]
    REASON_FIELD_NUMBER: _ClassVar[int]
    start: _timestamp_pb2.Timestamp
    end: _timestamp_pb2.Timestamp
    reason: str
    def __init__(self, start: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., end: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., reason: _Optional[str] = ...) -> None: ...

class SubmitTaskRequest(_message.Message):
    __slots__ = ("target", "idempotency_key", "payload", "shots", "session_id", "deadline", "native_limits", "parameters", "tenant_hint")
    class NativeLimitsEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    class ParametersEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    TARGET_FIELD_NUMBER: _ClassVar[int]
    IDEMPOTENCY_KEY_FIELD_NUMBER: _ClassVar[int]
    PAYLOAD_FIELD_NUMBER: _ClassVar[int]
    SHOTS_FIELD_NUMBER: _ClassVar[int]
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    DEADLINE_FIELD_NUMBER: _ClassVar[int]
    NATIVE_LIMITS_FIELD_NUMBER: _ClassVar[int]
    PARAMETERS_FIELD_NUMBER: _ClassVar[int]
    TENANT_HINT_FIELD_NUMBER: _ClassVar[int]
    target: TargetRef
    idempotency_key: str
    payload: Payload
    shots: int
    session_id: str
    deadline: _timestamp_pb2.Timestamp
    native_limits: _containers.ScalarMap[str, str]
    parameters: _containers.ScalarMap[str, str]
    tenant_hint: str
    def __init__(self, target: _Optional[_Union[TargetRef, _Mapping]] = ..., idempotency_key: _Optional[str] = ..., payload: _Optional[_Union[Payload, _Mapping]] = ..., shots: _Optional[int] = ..., session_id: _Optional[str] = ..., deadline: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., native_limits: _Optional[_Mapping[str, str]] = ..., parameters: _Optional[_Mapping[str, str]] = ..., tenant_hint: _Optional[str] = ...) -> None: ...

class Payload(_message.Message):
    __slots__ = ("format", "inline", "uri")
    FORMAT_FIELD_NUMBER: _ClassVar[int]
    INLINE_FIELD_NUMBER: _ClassVar[int]
    URI_FIELD_NUMBER: _ClassVar[int]
    format: str
    inline: bytes
    uri: str
    def __init__(self, format: _Optional[str] = ..., inline: _Optional[bytes] = ..., uri: _Optional[str] = ...) -> None: ...

class TaskHandle(_message.Message):
    __slots__ = ("target", "task_id")
    TARGET_FIELD_NUMBER: _ClassVar[int]
    TASK_ID_FIELD_NUMBER: _ClassVar[int]
    target: TargetRef
    task_id: str
    def __init__(self, target: _Optional[_Union[TargetRef, _Mapping]] = ..., task_id: _Optional[str] = ...) -> None: ...

class TaskRef(_message.Message):
    __slots__ = ("target", "task_id")
    TARGET_FIELD_NUMBER: _ClassVar[int]
    TASK_ID_FIELD_NUMBER: _ClassVar[int]
    target: TargetRef
    task_id: str
    def __init__(self, target: _Optional[_Union[TargetRef, _Mapping]] = ..., task_id: _Optional[str] = ...) -> None: ...

class TaskStatus(_message.Message):
    __slots__ = ("task", "state", "error", "result", "usage", "updated_at")
    class State(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
        __slots__ = ()
        STATE_UNSPECIFIED: _ClassVar[TaskStatus.State]
        QUEUED: _ClassVar[TaskStatus.State]
        RUNNING: _ClassVar[TaskStatus.State]
        SUCCEEDED: _ClassVar[TaskStatus.State]
        FAILED: _ClassVar[TaskStatus.State]
        CANCELLED: _ClassVar[TaskStatus.State]
    STATE_UNSPECIFIED: TaskStatus.State
    QUEUED: TaskStatus.State
    RUNNING: TaskStatus.State
    SUCCEEDED: TaskStatus.State
    FAILED: TaskStatus.State
    CANCELLED: TaskStatus.State
    TASK_FIELD_NUMBER: _ClassVar[int]
    STATE_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    RESULT_FIELD_NUMBER: _ClassVar[int]
    USAGE_FIELD_NUMBER: _ClassVar[int]
    UPDATED_AT_FIELD_NUMBER: _ClassVar[int]
    task: TaskRef
    state: TaskStatus.State
    error: ErrorDetail
    result: Result
    usage: _containers.RepeatedCompositeFieldContainer[UsageRecord]
    updated_at: _timestamp_pb2.Timestamp
    def __init__(self, task: _Optional[_Union[TaskRef, _Mapping]] = ..., state: _Optional[_Union[TaskStatus.State, str]] = ..., error: _Optional[_Union[ErrorDetail, _Mapping]] = ..., result: _Optional[_Union[Result, _Mapping]] = ..., usage: _Optional[_Iterable[_Union[UsageRecord, _Mapping]]] = ..., updated_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class Result(_message.Message):
    __slots__ = ("format", "inline", "uri", "metadata")
    class MetadataEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    FORMAT_FIELD_NUMBER: _ClassVar[int]
    INLINE_FIELD_NUMBER: _ClassVar[int]
    URI_FIELD_NUMBER: _ClassVar[int]
    METADATA_FIELD_NUMBER: _ClassVar[int]
    format: str
    inline: bytes
    uri: str
    metadata: _containers.ScalarMap[str, str]
    def __init__(self, format: _Optional[str] = ..., inline: _Optional[bytes] = ..., uri: _Optional[str] = ..., metadata: _Optional[_Mapping[str, str]] = ...) -> None: ...

class UsageRecord(_message.Message):
    __slots__ = ("unit", "amount", "recorded_at")
    UNIT_FIELD_NUMBER: _ClassVar[int]
    AMOUNT_FIELD_NUMBER: _ClassVar[int]
    RECORDED_AT_FIELD_NUMBER: _ClassVar[int]
    unit: str
    amount: float
    recorded_at: _timestamp_pb2.Timestamp
    def __init__(self, unit: _Optional[str] = ..., amount: _Optional[float] = ..., recorded_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class CancelTaskResponse(_message.Message):
    __slots__ = ("accepted",)
    ACCEPTED_FIELD_NUMBER: _ClassVar[int]
    accepted: bool
    def __init__(self, accepted: _Optional[bool] = ...) -> None: ...

class OpenSessionRequest(_message.Message):
    __slots__ = ("target", "max_duration", "tenant_hint")
    TARGET_FIELD_NUMBER: _ClassVar[int]
    MAX_DURATION_FIELD_NUMBER: _ClassVar[int]
    TENANT_HINT_FIELD_NUMBER: _ClassVar[int]
    target: TargetRef
    max_duration: _duration_pb2.Duration
    tenant_hint: str
    def __init__(self, target: _Optional[_Union[TargetRef, _Mapping]] = ..., max_duration: _Optional[_Union[datetime.timedelta, _duration_pb2.Duration, _Mapping]] = ..., tenant_hint: _Optional[str] = ...) -> None: ...

class SessionHandle(_message.Message):
    __slots__ = ("target", "session_id", "expires_at")
    TARGET_FIELD_NUMBER: _ClassVar[int]
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    EXPIRES_AT_FIELD_NUMBER: _ClassVar[int]
    target: TargetRef
    session_id: str
    expires_at: _timestamp_pb2.Timestamp
    def __init__(self, target: _Optional[_Union[TargetRef, _Mapping]] = ..., session_id: _Optional[str] = ..., expires_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class CloseSessionResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class ErrorDetail(_message.Message):
    __slots__ = ("category", "retriable", "vendor_code", "vendor_message")
    class Category(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
        __slots__ = ()
        CATEGORY_UNSPECIFIED: _ClassVar[ErrorDetail.Category]
        INVALID_PROGRAM: _ClassVar[ErrorDetail.Category]
        CAPABILITY_MISMATCH: _ClassVar[ErrorDetail.Category]
        DEVICE_OFFLINE: _ClassVar[ErrorDetail.Category]
        CALIBRATION_STALE: _ClassVar[ErrorDetail.Category]
        CAPACITY_EXHAUSTED: _ClassVar[ErrorDetail.Category]
        BUDGET_EXCEEDED: _ClassVar[ErrorDetail.Category]
        SESSION_LOST: _ClassVar[ErrorDetail.Category]
        VENDOR_ERROR: _ClassVar[ErrorDetail.Category]
    CATEGORY_UNSPECIFIED: ErrorDetail.Category
    INVALID_PROGRAM: ErrorDetail.Category
    CAPABILITY_MISMATCH: ErrorDetail.Category
    DEVICE_OFFLINE: ErrorDetail.Category
    CALIBRATION_STALE: ErrorDetail.Category
    CAPACITY_EXHAUSTED: ErrorDetail.Category
    BUDGET_EXCEEDED: ErrorDetail.Category
    SESSION_LOST: ErrorDetail.Category
    VENDOR_ERROR: ErrorDetail.Category
    CATEGORY_FIELD_NUMBER: _ClassVar[int]
    RETRIABLE_FIELD_NUMBER: _ClassVar[int]
    VENDOR_CODE_FIELD_NUMBER: _ClassVar[int]
    VENDOR_MESSAGE_FIELD_NUMBER: _ClassVar[int]
    category: ErrorDetail.Category
    retriable: bool
    vendor_code: str
    vendor_message: str
    def __init__(self, category: _Optional[_Union[ErrorDetail.Category, str]] = ..., retriable: _Optional[bool] = ..., vendor_code: _Optional[str] = ..., vendor_message: _Optional[str] = ...) -> None: ...
