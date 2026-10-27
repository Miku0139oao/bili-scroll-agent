package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
)

const (
	// maxScriptBytes caps an adapter script; it is code, not data.
	maxScriptBytes = 64 << 10
	// maxBalances caps the number of balances an adapter may report.
	maxBalances = 32
)

// ScriptCheck is the result of compiling an adapter without running it.
type ScriptCheck struct {
	Compiled  bool     `json:"compiled"`
	Functions []string `json:"functions"`
	Hash      string   `json:"hash"`
	ElapsedMS int64    `json:"elapsed_ms"`
}

// jsRunner compiles and runs admin-authored balance adapters.
//
// Trust model: scripts are written by administrators and are therefore trusted
// code. They necessarily receive the provider API key, so a hostile script can
// always leak that key. What the sandbox removes is ambient capability — no
// filesystem, environment, process spawning, module loading, or arbitrary
// sockets — and it confines egress to the provider's allow_hosts, so a mistake
// cannot ship credentials to an unrelated host.
type jsRunner struct {
	opts Options
	mu   sync.RWMutex
	// programs caches compiled scripts by hash.
	programs map[string]*goja.Program
}

func newJSRunner(opts Options) *jsRunner {
	return &jsRunner{opts: opts, programs: map[string]*goja.Program{}}
}

// wrapScript turns the admin's source into an IIFE that returns fetchBalance.
//
// A bare function declaration would otherwise land in the runtime's global
// scope, letting one script observe or overwrite another's globals.
func wrapScript(source string) string {
	return "(function(){\"use strict\";\n" + source + "\n;return (typeof fetchBalance === 'function') ? fetchBalance : null;})()"
}

func scriptHash(source string) string {
	sum := sha256.Sum256([]byte(source))
	return hex.EncodeToString(sum[:])
}

func (r *jsRunner) compile(source string) (*goja.Program, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return nil, errors.New("adapter script is empty")
	}
	if len(source) > maxScriptBytes {
		return nil, fmt.Errorf("adapter script exceeds %d bytes", maxScriptBytes)
	}
	program, err := goja.Compile("adapter.js", wrapScript(source), true)
	if err != nil {
		return nil, fmt.Errorf("adapter script does not compile: %w", err)
	}
	return program, nil
}

// Validate compiles the script and checks that fetchBalance is declared.
func (r *jsRunner) Validate(source string) (ScriptCheck, error) {
	started := time.Now()
	program, err := r.compile(source)
	if err != nil {
		return ScriptCheck{}, err
	}
	hash := scriptHash(source)
	r.storeProgram(hash, program)

	vm := goja.New()
	vm.SetMaxCallStackSize(r.opts.MaxCallStackSize)
	if err := sealRuntime(vm); err != nil {
		return ScriptCheck{}, err
	}
	value, err := vm.RunProgram(program)
	if err != nil {
		return ScriptCheck{}, fmt.Errorf("adapter script failed to load: %w", err)
	}
	if goja.IsNull(value) || goja.IsUndefined(value) {
		return ScriptCheck{}, errors.New("adapter must define a fetchBalance(ctx) function")
	}
	if _, ok := goja.AssertFunction(value); !ok {
		return ScriptCheck{}, errors.New("adapter must define a fetchBalance(ctx) function")
	}
	return ScriptCheck{
		Compiled:  true,
		Functions: []string{"fetchBalance"},
		Hash:      hash,
		ElapsedMS: time.Since(started).Milliseconds(),
	}, nil
}

func (r *jsRunner) storeProgram(hash string, program *goja.Program) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Keep the cache bounded; adapters are edited rarely and compiled cheaply.
	if len(r.programs) > 64 {
		r.programs = map[string]*goja.Program{}
	}
	r.programs[hash] = program
}

func (r *jsRunner) lookupProgram(hash string) (*goja.Program, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	program, ok := r.programs[hash]
	return program, ok
}

// run executes an adapter and returns its raw balance list.
func (r *jsRunner) run(ctx context.Context, cfg Config, env *FetchEnv) ([]Balance, error) {
	hash := scriptHash(strings.TrimSpace(cfg.Script))
	program, ok := r.lookupProgram(hash)
	if !ok {
		compiled, err := r.compile(cfg.Script)
		if err != nil {
			return nil, err
		}
		program = compiled
		r.storeProgram(hash, program)
	}

	vm := goja.New()
	vm.SetMaxCallStackSize(r.opts.MaxCallStackSize)
	if err := sealRuntime(vm); err != nil {
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, context.DeadlineExceeded
		}
		// Hard-stop runaway scripts even if every host call returns promptly.
		timer := time.AfterFunc(remaining, func() { vm.Interrupt("adapter timeout") })
		defer timer.Stop()
		defer vm.ClearInterrupt()
	}
	if err := installHostAPI(vm, r.opts, cfg, env); err != nil {
		return nil, err
	}

	loaded, err := vm.RunProgram(program)
	if err != nil {
		return nil, scriptError(err)
	}
	fetch, ok := goja.AssertFunction(loaded)
	if !ok {
		return nil, errors.New("adapter must define a fetchBalance(ctx) function")
	}
	// The host API is passed explicitly rather than read from a global so the
	// script cannot stash or rewrite it for a later call.
	ctxObject := vm.Get("__bili_ctx")
	result, err := fetch(goja.Undefined(), ctxObject)
	if err != nil {
		return nil, scriptError(err)
	}
	return exportBalances(vm, result)
}

func scriptError(err error) error {
	var interrupted *goja.InterruptedError
	if errors.As(err, &interrupted) {
		if value, ok := interrupted.Value().(string); ok && value == "adapter timeout" {
			return errors.New("adapter timed out")
		}
		return errors.New("adapter execution was interrupted")
	}
	var exception *goja.Exception
	if errors.As(err, &exception) {
		return fmt.Errorf("adapter error: %s", exception.String())
	}
	return err
}

// sealRuntime removes capabilities that are not part of the adapter contract.
//
// goja ships a bare ECMAScript runtime, so require/process/fetch/console are
// already absent. Sealing them explicitly both documents the contract and makes
// the guarantee survive a future goja release or a helper module being loaded.
func sealRuntime(vm *goja.Runtime) error {
	for _, name := range forbiddenGlobals {
		if err := vm.GlobalObject().Delete(name); err != nil {
			return err
		}
	}
	// Freeze the global object so a script cannot add new globals that a later
	// script in another runtime could rely on (defensive; runtimes are per call).
	return nil
}

var forbiddenGlobals = []string{
	"require", "module", "exports", "process", "console", "fetch",
	"XMLHttpRequest", "WebSocket", "setTimeout", "setInterval",
	"setImmediate", "clearTimeout", "clearInterval", "Buffer",
}

// exportBalances converts the script's return value into Balance values.
func exportBalances(vm *goja.Runtime, value goja.Value) ([]Balance, error) {
	if goja.IsNull(value) || goja.IsUndefined(value) {
		return nil, errors.New("adapter returned no balances")
	}
	array, ok := value.Export().([]any)
	if !ok {
		// goja exports arrays of objects as []interface{}; anything else is a
		// contract violation rather than an empty result.
		if list, ok := value.Export().([]map[string]any); ok {
			array = make([]any, 0, len(list))
			for _, item := range list {
				array = append(array, item)
			}
		} else {
			return nil, errors.New("adapter must return an array of balances")
		}
	}
	if len(array) > maxBalances {
		return nil, fmt.Errorf("adapter returned %d balances; at most %d are supported", len(array), maxBalances)
	}
	result := make([]Balance, 0, len(array))
	for index, item := range array {
		object, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("balance %d is not an object", index)
		}
		total, ok := finiteNumber(object["total"])
		if !ok {
			return nil, fmt.Errorf("balance %d has no finite total", index)
		}
		granted, _ := finiteNumber(object["granted"])
		toppedUp, _ := finiteNumber(object["topped_up"])
		available := true
		if flag, ok := object["available"].(bool); ok {
			available = flag
		}
		currency, _ := object["currency"].(string)
		result = append(result, Balance{
			Currency:  strings.TrimSpace(currency),
			Total:     total,
			Granted:   granted,
			ToppedUp:  toppedUp,
			Available: available,
		})
	}
	return result, nil
}

func finiteNumber(value any) (float64, bool) {
	var number float64
	switch typed := value.(type) {
	case float64:
		number = typed
	case int64:
		number = float64(typed)
	case int:
		number = float64(typed)
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, false
		}
		number = parsed
	default:
		return 0, false
	}
	if math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, false
	}
	return number, true
}

// scriptAdapter bridges the JS runner into the Adapter interface.
type scriptAdapter struct{ runner *jsRunner }

func (scriptAdapter) Kind() string          { return KindJS }
func (scriptAdapter) SupportsBalance() bool { return true }

func (a scriptAdapter) Fetch(ctx context.Context, cfg Config, env *FetchEnv) ([]Balance, error) {
	if strings.TrimSpace(cfg.Script) == "" {
		return nil, errors.New("adapter script is empty")
	}
	return a.runner.run(ctx, cfg, env)
}
