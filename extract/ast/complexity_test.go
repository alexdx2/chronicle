package ast

import "testing"

// TestExtractComplexity_Basic pins the exact Tier-A metrics for two free
// functions: a trivial one and a nested-loop one. These are exact AST counts
// (confidence 1.0), so the expected numbers are not approximate.
func TestExtractComplexity_Basic(t *testing.T) {
	src := []byte(`
function simple() {
  return 1;
}

function withLoops(items: number[]) {
  for (const i of items) {
    while (i > 0) {
      if (i % 2 === 0) {
        doThing();
      }
    }
  }
}
`)

	got := ExtractComplexity(src)
	byName := map[string]FunctionComplexity{}
	for _, fc := range got {
		byName[fc.Name] = fc
	}

	simple, ok := byName["simple"]
	if !ok {
		t.Fatalf("simple not found; got %+v", got)
	}
	if simple.Cyclomatic != 1 {
		t.Errorf("simple cyclomatic = %d, want 1", simple.Cyclomatic)
	}
	if simple.LoopCount != 0 {
		t.Errorf("simple loop_count = %d, want 0", simple.LoopCount)
	}
	if simple.LoopDepth != 0 {
		t.Errorf("simple loop_depth = %d, want 0", simple.LoopDepth)
	}

	wl, ok := byName["withLoops"]
	if !ok {
		t.Fatalf("withLoops not found; got %+v", got)
	}
	// decision points: for-of (1) + while (1) + if (1) = 3 -> cyclomatic 4
	if wl.Cyclomatic != 4 {
		t.Errorf("withLoops cyclomatic = %d, want 4", wl.Cyclomatic)
	}
	// for-of + while = 2 loops
	if wl.LoopCount != 2 {
		t.Errorf("withLoops loop_count = %d, want 2", wl.LoopCount)
	}
	// for nested while -> depth 2 (the if is not a loop)
	if wl.LoopDepth != 2 {
		t.Errorf("withLoops loop_depth = %d, want 2", wl.LoopDepth)
	}
	// span should cover more than one line
	if wl.EndLine <= wl.StartLine {
		t.Errorf("withLoops span = %d..%d, want multi-line", wl.StartLine, wl.EndLine)
	}
}

// TestExtractComplexity_MethodAndLogicalOps proves class methods are measured
// and that short-circuit boolean operators (&&, ||, ??) and switch cases count
// toward cyclomatic (the ESLint/McCabe convention), while a nested function's
// own complexity does not leak into its enclosing method.
func TestExtractComplexity_MethodAndLogicalOps(t *testing.T) {
	src := []byte(`
class Svc {
  handle(a, b) {
    if (a && b || a) {
      return 1;
    }
    switch (a) {
      case 1: return 1;
      case 2: return 2;
      default: return 0;
    }
    const inner = () => {
      for (const x of b) { doThing(); }
    };
    inner();
  }
}
`)

	byName := map[string]FunctionComplexity{}
	for _, fc := range ExtractComplexity(src) {
		byName[fc.Name] = fc
	}

	h, ok := byName["handle"]
	if !ok {
		t.Fatalf("handle method not found; got %+v", byName)
	}
	// decision points: if (1) + && (1) + || (1) + case 1 (1) + case 2 (1) = 5
	// default is NOT counted; the inner arrow's `for` belongs to the arrow, not handle.
	if h.Cyclomatic != 6 {
		t.Errorf("handle cyclomatic = %d, want 6", h.Cyclomatic)
	}
	// handle has no loops of its own (the for is inside the nested arrow).
	if h.LoopCount != 0 {
		t.Errorf("handle loop_count = %d, want 0 (nested arrow's loop must not leak)", h.LoopCount)
	}
	if h.LoopDepth != 0 {
		t.Errorf("handle loop_depth = %d, want 0", h.LoopDepth)
	}
}
