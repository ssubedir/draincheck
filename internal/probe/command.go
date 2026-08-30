package probe

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ssubedir/draincheck/internal/traffic"
)

const (
	ProtocolVersion = "1"
	PhaseInitial    = "initial"
	PhasePostSignal = "post_signal"

	maxProtocolBytes = 64 << 10
	maxProtocolLine  = 8 << 10
	maxStderrBytes   = 16 << 10
	maxMessageBytes  = 300
)

type Spec struct {
	Executable  string
	Args        []string
	Directory   string
	Environment map[string]string
	BaseURL     string
	RunID       string
	Phase       string
	Count       int
	Concurrency int
	Timeout     time.Duration
}

type Run struct {
	ctx     context.Context
	spec    Spec
	done    chan struct{}
	started chan struct{}
	one     sync.Once

	mu      sync.Mutex
	stopped bool
	active  map[int]struct{}
	results map[int]traffic.Result
	count   int
}

func Start(ctx context.Context, spec Spec) *Run {
	spec.Args = append([]string(nil), spec.Args...)
	spec.Environment = cloneStrings(spec.Environment)
	run := &Run{
		ctx:     ctx,
		spec:    spec,
		done:    make(chan struct{}),
		started: make(chan struct{}),
		active:  make(map[int]struct{}),
		results: make(map[int]traffic.Result),
	}
	jobs := make(chan int, spec.Count)
	for id := 1; id <= spec.Count; id++ {
		jobs <- id
	}
	close(jobs)

	var workers sync.WaitGroup
	workers.Add(spec.Concurrency)
	for range spec.Concurrency {
		go func() {
			defer workers.Done()
			for id := range jobs {
				if !run.canLaunch() {
					continue
				}
				run.finish(id, run.execute(id))
			}
		}()
	}
	go func() {
		workers.Wait()
		close(run.done)
	}()
	return run
}

func (r *Run) WaitStarted(ctx context.Context) error {
	if r.StartedCount() > 0 {
		return nil
	}
	select {
	case <-r.started:
		return nil
	case <-r.done:
		if r.StartedCount() > 0 {
			return nil
		}
		return errors.New("command probes finished before reporting active work")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Run) StopAndSnapshot() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopped = true
	return r.activeSnapshotLocked()
}

func (r *Run) ActiveSnapshot() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.activeSnapshotLocked()
}

func (r *Run) Done() <-chan struct{} { return r.done }

func (r *Run) Results() []traffic.Result {
	r.mu.Lock()
	defer r.mu.Unlock()
	results := make([]traffic.Result, 0, len(r.results))
	for _, result := range r.results {
		results = append(results, result)
	}
	sort.Slice(results, func(i, j int) bool { return results[i].ID < results[j].ID })
	return results
}

func (r *Run) StartedCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.count
}

func (r *Run) canLaunch() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.stopped
}

func (r *Run) activate(id int) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return false
	}
	if _, found := r.active[id]; found {
		return false
	}
	r.active[id] = struct{}{}
	r.count++
	r.one.Do(func() { close(r.started) })
	return true
}

func (r *Run) finish(id int, result traffic.Result) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.active, id)
	r.results[id] = result
}

func (r *Run) activeSnapshotLocked() []int {
	ids := make([]int, 0, len(r.active))
	for id := range r.active {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

type protocolEvent struct {
	Type    string `json:"type"`
	Success *bool  `json:"success,omitempty"`
	Message string `json:"message,omitempty"`
}

type protocolState struct {
	active        bool
	result        bool
	resultSuccess bool
	message       string
	errorKind     string
	errorMessage  string
}

func (r *Run) execute(id int) traffic.Result {
	started := time.Now()
	result := traffic.Result{ID: id, StartedAt: started}
	ctx, cancel := context.WithTimeout(r.ctx, r.spec.Timeout)
	defer cancel()

	// Running the explicitly configured probe command is the feature under test.
	command := exec.CommandContext(ctx, r.spec.Executable, r.spec.Args...) // #nosec G204 -- The contract author controls this local command.
	command.Dir = r.spec.Directory
	command.Env = commandEnvironment(r.spec, id)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return commandFailure(result, started, "command_pipe", "could not create command stdout")
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return commandFailure(result, started, "command_pipe", "could not create command stderr")
	}
	if err := command.Start(); err != nil {
		return commandFailure(result, started, "command_start", "could not start command probe")
	}
	stderrResult := make(chan string, 1)
	go func() { stderrResult <- readBounded(stderr, maxStderrBytes) }()

	state := r.readProtocol(id, stdout)
	if state.errorKind != "" {
		cancel()
	}
	waitErr := command.Wait()
	stderrText := <-stderrResult

	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return commandFailure(result, started, "timeout", "command probe exceeded its request timeout")
	}
	if r.ctx.Err() != nil {
		return commandFailure(result, started, "canceled", "command probe was canceled")
	}
	if state.errorKind != "" {
		return commandFailure(result, started, state.errorKind, state.errorMessage)
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return commandFailure(result, started, "canceled", "command probe was canceled")
	}
	if !state.active {
		return commandFailure(result, started, "protocol_missing_active", "command probe exited without an active event")
	}
	if !state.result {
		return commandFailure(result, started, "protocol_missing_result", "command probe exited without a result event")
	}
	if waitErr != nil {
		message := "command probe exited non-zero"
		if stderrText != "" {
			message += ": " + stderrText
		}
		return commandFailure(result, started, "exit_code", message)
	}
	result.Duration = time.Since(started)
	result.Success = state.resultSuccess
	if !result.Success {
		result.ErrorKind = "probe_result"
		result.Error = state.message
		if result.Error == "" {
			result.Error = "command probe reported failure"
		}
	}
	return result
}

func (r *Run) readProtocol(id int, reader io.Reader) protocolState {
	limited := &io.LimitedReader{R: reader, N: maxProtocolBytes + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 1024), maxProtocolLine)
	state := protocolState{}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		event, kind, message := decodeEvent(line)
		if kind != "" {
			state.errorKind, state.errorMessage = kind, message
			return state
		}
		switch event.Type {
		case "active":
			if state.active || state.result || event.Success != nil || event.Message != "" {
				state.errorKind = "protocol_order"
				state.errorMessage = "active event was duplicated, late, or included result fields"
				return state
			}
			if !r.activate(id) {
				state.errorKind = "protocol_late_active"
				state.errorMessage = "active event arrived after traffic scheduling stopped"
				return state
			}
			state.active = true
		case "result":
			if !state.active || state.result || event.Success == nil {
				state.errorKind = "protocol_order"
				state.errorMessage = "result event was early, duplicated, or missing success"
				return state
			}
			state.result = true
			state.resultSuccess = *event.Success
			state.message = bound(event.Message, maxMessageBytes)
		default:
			state.errorKind = "protocol_event"
			state.errorMessage = "command probe emitted an unknown event type"
			return state
		}
	}
	if limited.N <= 0 {
		state.errorKind = "protocol_output_limit"
		state.errorMessage = "command probe stdout exceeded 64 KiB"
		return state
	}
	if err := scanner.Err(); err != nil {
		if strings.Contains(err.Error(), "token too long") {
			state.errorKind = "protocol_line_limit"
			state.errorMessage = "command probe emitted a line larger than 8 KiB"
		} else {
			state.errorKind = "protocol_read"
			state.errorMessage = "could not read command probe protocol output"
		}
	}
	return state
}

func decodeEvent(line string) (protocolEvent, string, string) {
	var event protocolEvent
	decoder := json.NewDecoder(strings.NewReader(line))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&event); err != nil {
		return protocolEvent{}, "protocol_json", "command probe emitted malformed JSON"
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return protocolEvent{}, "protocol_json", "command probe emitted multiple JSON values on one line"
	}
	if len(event.Message) > maxMessageBytes {
		return protocolEvent{}, "protocol_message_limit", "command probe message exceeded 300 bytes"
	}
	return event, "", ""
}

func commandFailure(result traffic.Result, started time.Time, kind, message string) traffic.Result {
	result.Duration = time.Since(started)
	result.ErrorKind = kind
	result.Error = bound(strings.TrimSpace(message), maxMessageBytes)
	return result
}

func readBounded(reader io.Reader, limit int64) string {
	var buffer bytes.Buffer
	_, _ = io.Copy(&buffer, io.LimitReader(reader, limit))
	_, _ = io.Copy(io.Discard, reader)
	return bound(strings.TrimSpace(buffer.String()), maxMessageBytes)
}

func commandEnvironment(spec Spec, id int) []string {
	type entry struct{ name, value string }
	values := make(map[string]entry)
	set := func(name, value string) {
		key := name
		if runtime.GOOS == "windows" {
			key = strings.ToUpper(name)
		}
		values[key] = entry{name: name, value: value}
	}
	for _, current := range os.Environ() {
		name, value, found := strings.Cut(current, "=")
		if found {
			set(name, value)
		}
	}
	for name, value := range spec.Environment {
		set(name, value)
	}
	set("DRAINCHECK_PROTOCOL_VERSION", ProtocolVersion)
	set("DRAINCHECK_TARGET_URL", spec.BaseURL)
	set("DRAINCHECK_RUN_ID", spec.RunID)
	set("DRAINCHECK_REQUEST_ID", strconv.Itoa(id))
	set("DRAINCHECK_PHASE", spec.Phase)

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	environment := make([]string, 0, len(keys))
	for _, key := range keys {
		item := values[key]
		environment = append(environment, item.name+"="+item.value)
	}
	return environment
}

func cloneStrings(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func bound(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
