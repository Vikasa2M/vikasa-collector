package model

// ControllerMode is a signal controller's coordination mode: whether it is
// running a coordinated timing plan or running free. It says nothing about
// what the signal heads show — that is CabinetOutput (ADR 0024).
type ControllerMode uint8

// ControllerMode values. The zero value, ModeUnknown, means the adapter did
// not read coordination (ntcip-asc does not, today).
const (
	ModeUnknown ControllerMode = iota
	ModeCoordinated
	ModeFree
)

func (m ControllerMode) String() string {
	switch m {
	case ModeCoordinated:
		return "coordinated"
	case ModeFree:
		return "free"
	default:
		return "unknown"
	}
}

// CabinetOutput is what the signal heads are doing: normal operation,
// flashing, or dark. It is independent of coordination: a coordinated
// controller under monitor flash is ModeCoordinated with OutputFlash.
type CabinetOutput uint8

// CabinetOutput values. The zero value, OutputUnknown, means not reported.
const (
	OutputUnknown CabinetOutput = iota
	OutputNormal
	OutputFlash
	OutputDark
)

func (o CabinetOutput) String() string {
	switch o {
	case OutputNormal:
		return "normal"
	case OutputFlash:
		return "flash"
	case OutputDark:
		return "dark"
	default:
		return "unknown"
	}
}

// FlashCause is why the cabinet is flashing. Meaningful only with
// OutputFlash; the zero value, FlashCauseUnknown, means not reported.
type FlashCause uint8

// FlashCause values.
const (
	FlashCauseUnknown    FlashCause = iota
	FlashMonitor                    // the conflict monitor / MMU forced flash
	FlashControllerFault            // the controller's own fault monitor
	FlashAutomatic                  // automatic flash commanded by the controller
	FlashManual                     // a technician's manual flash switch
	FlashStartup                    // start-up flash
	FlashPreempt                    // flash as part of a preemption sequence
	FlashScheduled                  // programmed time-of-day flash
	FlashOther                      // vendor-specific
)

func (c FlashCause) String() string {
	switch c {
	case FlashMonitor:
		return "monitor"
	case FlashControllerFault:
		return "controller-fault"
	case FlashAutomatic:
		return "automatic"
	case FlashManual:
		return "manual"
	case FlashStartup:
		return "startup"
	case FlashPreempt:
		return "preempt"
	case FlashScheduled:
		return "scheduled"
	case FlashOther:
		return "other"
	default:
		return "unknown"
	}
}

// FaultSeverity is the collector-owned severity enum. Its order mirrors the
// catalog's FAULT_SEVERITY_* (INFO=0..CRITICAL=4) so the wire mapping is a
// straight table — but the type is ours and does not move when upstream
// renumbers, which is the whole point of ADR 0002.
type FaultSeverity uint8

// FaultSeverity values, in increasing severity. There is no unknown: the zero
// value is SeverityInfo, as the catalog's FAULT_SEVERITY_INFO is 0.
const (
	SeverityInfo FaultSeverity = iota
	SeverityWarning
	SeverityMinor
	SeverityMajor
	SeverityCritical
)

func (s FaultSeverity) String() string {
	switch s {
	case SeverityInfo:
		return "info"
	case SeverityWarning:
		return "warning"
	case SeverityMinor:
		return "minor"
	case SeverityMajor:
		return "major"
	case SeverityCritical:
		return "critical"
	default:
		return "unknown"
	}
}

// FaultCategory groups faults by cause. The catalog has NO category on its
// fault events — it folded category into a free-form `kind` string, keeping
// Category only on the state Fault message. We keep it anyway: the domain
// model may be richer than any wire version, and mapping-or-dropping it is a
// single emitter's decision (ADR 0002) rather than a collection blocker.
type FaultCategory uint8

// FaultCategory values. The zero value, CategoryUnknown, means the fault did
// not map to a known category.
const (
	CategoryUnknown FaultCategory = iota
	CategoryConflict
	CategoryCabinet
	CategoryPower
	CategoryCommunication
	CategoryDetector
	CategoryLamp
	CategoryPixel       // dms-fault-pixel
	CategoryController  // dms-fault-controller
	CategoryEnvironment // dms-fault-environment
)

func (c FaultCategory) String() string {
	switch c {
	case CategoryConflict:
		return "conflict"
	case CategoryCabinet:
		return "cabinet"
	case CategoryPower:
		return "power"
	case CategoryCommunication:
		return "communication"
	case CategoryDetector:
		return "detector"
	case CategoryLamp:
		return "lamp"
	case CategoryPixel:
		return "pixel"
	case CategoryController:
		return "controller"
	case CategoryEnvironment:
		return "environment"
	default:
		return "unknown"
	}
}

// DataQuality is the sensor's own assessment of a measurement.
//
// QualityUnknown is the zero value and means the sensor did not say. That is
// deliberately distinct from Valid: a reading nobody vouched for is not the
// same as one the device asserted was good, and the emitter has to make that
// distinction explicitly rather than inherit it from a struct default.
type DataQuality uint8

// DataQuality values. The zero value, QualityUnknown, means the device did
// not report quality.
const (
	QualityUnknown DataQuality = iota
	QualityValid
	QualitySuspect
	QualityInvalid
)

func (q DataQuality) String() string {
	switch q {
	case QualityValid:
		return "valid"
	case QualitySuspect:
		return "suspect"
	case QualityInvalid:
		return "invalid"
	default:
		return "unknown"
	}
}
