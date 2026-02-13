package logging

import (
	"testing"
)

func TestNewColoredLoggerReturnsNonNil(t *testing.T) {
	logger, err := NewColoredLogger(ComponentGeneral, true)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if logger == nil {
		t.Fatal("expected non-nil logger")
	}
}

func TestNewColoredLoggerNoColors(t *testing.T) {
	logger, err := NewColoredLogger(ComponentNode, false)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if logger == nil {
		t.Fatal("expected non-nil logger")
	}
}

func TestNewColoredLoggerAllComponents(t *testing.T) {
	components := []Component{
		ComponentNode,
		ComponentRQLite,
		ComponentLibP2P,
		ComponentStorage,
		ComponentDatabase,
		ComponentClient,
		ComponentGeneral,
		ComponentAnyone,
		ComponentGateway,
	}

	for _, comp := range components {
		t.Run(string(comp), func(t *testing.T) {
			logger, err := NewColoredLogger(comp, true)
			if err != nil {
				t.Fatalf("expected no error for component %s, got: %v", comp, err)
			}
			if logger == nil {
				t.Fatalf("expected non-nil logger for component %s", comp)
			}
		})
	}
}

func TestNewColoredLoggerCanLog(t *testing.T) {
	logger, err := NewColoredLogger(ComponentGeneral, false)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// These should not panic. Output goes to stdout which is acceptable in tests.
	logger.Info("test info message")
	logger.Warn("test warn message")
	logger.Error("test error message")
	logger.Debug("test debug message")
}

func TestNewDefaultLoggerReturnsNonNil(t *testing.T) {
	logger, err := NewDefaultLogger(ComponentNode)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if logger == nil {
		t.Fatal("expected non-nil logger")
	}
}

func TestNewDefaultLoggerCanLog(t *testing.T) {
	logger, err := NewDefaultLogger(ComponentDatabase)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	logger.Info("default logger info")
	logger.Warn("default logger warn")
	logger.Error("default logger error")
	logger.Debug("default logger debug")
}

func TestComponentInfoDoesNotPanic(t *testing.T) {
	logger, err := NewColoredLogger(ComponentNode, true)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Should not panic
	logger.ComponentInfo(ComponentNode, "node info message")
	logger.ComponentInfo(ComponentRQLite, "rqlite info message")
	logger.ComponentInfo(ComponentGateway, "gateway info message")
}

func TestComponentWarnDoesNotPanic(t *testing.T) {
	logger, err := NewColoredLogger(ComponentStorage, true)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	logger.ComponentWarn(ComponentStorage, "storage warning")
	logger.ComponentWarn(ComponentLibP2P, "libp2p warning")
}

func TestComponentErrorDoesNotPanic(t *testing.T) {
	logger, err := NewColoredLogger(ComponentDatabase, false)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	logger.ComponentError(ComponentDatabase, "database error")
	logger.ComponentError(ComponentAnyone, "anyone error")
}

func TestComponentDebugDoesNotPanic(t *testing.T) {
	logger, err := NewColoredLogger(ComponentClient, true)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	logger.ComponentDebug(ComponentClient, "client debug")
	logger.ComponentDebug(ComponentGeneral, "general debug")
}

func TestComponentMethodsWithoutColors(t *testing.T) {
	logger, err := NewColoredLogger(ComponentGateway, false)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// All component methods with colors disabled should not panic
	logger.ComponentInfo(ComponentGateway, "info no color")
	logger.ComponentWarn(ComponentGateway, "warn no color")
	logger.ComponentError(ComponentGateway, "error no color")
	logger.ComponentDebug(ComponentGateway, "debug no color")
}

func TestStandardLoggerPrintfDoesNotPanic(t *testing.T) {
	sl, err := NewStandardLogger(ComponentGeneral)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	sl.Printf("formatted message: %s %d", "hello", 42)
}

func TestStandardLoggerPrintDoesNotPanic(t *testing.T) {
	sl, err := NewStandardLogger(ComponentNode)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	sl.Print("simple message")
	sl.Print("multiple", " ", "args")
}

func TestStandardLoggerPrintlnDoesNotPanic(t *testing.T) {
	sl, err := NewStandardLogger(ComponentRQLite)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	sl.Println("line message")
	sl.Println("multiple", "args")
}

func TestStandardLoggerErrorfDoesNotPanic(t *testing.T) {
	sl, err := NewStandardLogger(ComponentStorage)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	sl.Errorf("error: %s", "something went wrong")
}

func TestStandardLoggerReturnsNonNil(t *testing.T) {
	sl, err := NewStandardLogger(ComponentAnyone)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if sl == nil {
		t.Fatal("expected non-nil StandardLogger")
	}
}

func TestGetComponentColorReturnsValue(t *testing.T) {
	// Test all known components return a non-empty color string
	components := []Component{
		ComponentNode,
		ComponentRQLite,
		ComponentLibP2P,
		ComponentStorage,
		ComponentDatabase,
		ComponentClient,
		ComponentGeneral,
		ComponentAnyone,
		ComponentGateway,
	}

	for _, comp := range components {
		color := getComponentColor(comp)
		if color == "" {
			t.Errorf("expected non-empty color for component %s", comp)
		}
	}
}

func TestGetComponentColorUnknownComponent(t *testing.T) {
	color := getComponentColor(Component("UNKNOWN"))
	if color != White {
		t.Errorf("expected White for unknown component, got %q", color)
	}
}
