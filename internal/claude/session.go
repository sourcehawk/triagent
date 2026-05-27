package claude

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/charmbracelet/log"
)

// stderrTailMax bounds how many bytes of claude's stderr we hold on to
// for surfacing in the exit error. ~4 KB is enough for the typical
// "claude: command failed: ..." panic / refusal line plus a short
// backtrace, without ballooning memory if claude misbehaves and spews.
const stderrTailMax = 4 * 1024

// Session drives one `claude` invocation at a time. After Start returns an
// event for a final result, the session-id is captured so Resume can
// continue the same conversation.
type Session struct {
	binary        string
	mcpConfigPath string
	allowedTools  []string
	sessionID     string

	// cwd, when non-empty, is set as the child claude's working
	// directory. Claude keys its on-disk session jsonl by a hash of the
	// cwd, so Resume only finds a prior session if we re-launch from
	// the same dir. Per-investigation rehydrate sets this to the
	// investigation's session dir.
	cwd string

	// env is appended to os.Environ() in run(). Lets the launcher pin
	// KUBECONFIG so the operator's shell-level kube context switches
	// do not leak into the agent.
	env []string

	// appendSystemPrompt, when non-empty, is forwarded to claude as
	// --append-system-prompt. The auto-mode operator agent uses this to
	// inject the operator-role umbrella skill so the agent's behaviour
	// is anchored even if claude's skill-discovery decides not to load
	// the on-disk SKILL.md.
	appendSystemPrompt string

	// model passes through as --model to the claude CLI. Empty inherits
	// claude's default. Populated from SessionOpts.Model at NewSession time.
	model string
}

// SessionOpts collects the optional knobs on top of the required
// mcpConfigPath + allowedTools. Zero-valued fields keep today's
// behaviour.
type SessionOpts struct {
	Cwd string
	Env []string // each entry "KEY=value"

	// AppendSystemPrompt is forwarded as --append-system-prompt. Empty
	// (the default) means no override — claude uses its built-in prompt.
	AppendSystemPrompt string

	// Model passes through as --model to the claude CLI. Empty (the
	// default) inherits claude's default. Used by the launcher to route
	// auxiliary sessions (e.g. operator-agent) to a different model from
	// the investigation main session if needed.
	Model string
}

// NewSession locates the `claude` binary on PATH and validates it before
// returning. The returned session has no conversation yet; use Start.
func NewSession(mcpConfigPath string, allowedTools []string, opts ...SessionOpts) (*Session, error) {
	bin, err := exec.LookPath("claude")
	if err != nil {
		return nil, fmt.Errorf("`claude` CLI not found on PATH: %w (install from https://docs.claude.com/en/docs/claude-code)", err)
	}
	s := &Session{binary: bin, mcpConfigPath: mcpConfigPath, allowedTools: allowedTools}
	if len(opts) > 0 {
		s.cwd = opts[0].Cwd
		s.env = append([]string(nil), opts[0].Env...)
		s.appendSystemPrompt = opts[0].AppendSystemPrompt
		s.model = opts[0].Model
	}
	return s, nil
}

// RestoreSessionID seeds the session id captured in a prior launcher
// process. Lets Resume work without first calling Start. No-op when id
// is empty.
func (s *Session) RestoreSessionID(id string) {
	if id != "" {
		s.sessionID = id
	}
}

// SessionID returns the captured session id, or empty if Start has not
// produced one yet.
func (s *Session) SessionID() string { return s.sessionID }

// HasSessionID reports whether Start has produced a session id yet.
func (s *Session) HasSessionID() bool { return s.sessionID != "" }

// Model returns the configured model id (empty inherits claude's default).
// Exposed for tests; production code does not need to read it back.
func (s *Session) Model() string { return s.model }

// Start launches a fresh claude session with the given opening prompt.
// Stream-json events are delivered on the returned channel; the channel
// closes when claude exits or ctx is cancelled.
func (s *Session) Start(ctx context.Context, prompt string) (<-chan Event, error) {
	args := s.baseArgs()
	args = append(args, "--print")
	return s.run(ctx, args, prompt)
}

// Resume continues the most-recent session. Must be called after a prior
// Start has captured a session id.
func (s *Session) Resume(ctx context.Context, prompt string) (<-chan Event, error) {
	if s.sessionID == "" {
		return nil, fmt.Errorf("no prior session to resume")
	}
	args := s.baseArgs()
	args = append(args, "--resume", s.sessionID, "--print")
	return s.run(ctx, args, prompt)
}

func (s *Session) baseArgs() []string {
	args := []string{"--output-format", "stream-json", "--verbose"}
	if s.mcpConfigPath != "" {
		args = append(args, "--mcp-config", s.mcpConfigPath)
	}
	for _, pat := range s.allowedTools {
		args = append(args, "--allowedTools", pat)
	}
	if s.model != "" {
		args = append(args, "--model", s.model)
	}
	if s.appendSystemPrompt != "" {
		args = append(args, "--append-system-prompt", s.appendSystemPrompt)
	}
	return args
}

// run execs claude with args, feeds the user prompt over stdin (the
// safe path — passing a prompt as a positional argument is fragile
// because claude's CLI parser will read a leading "-" as an unknown
// option and exit non-zero), streams its stdout as Events onto the
// returned channel, and mirrors its stderr into the charmbracelet
// logger. The channel is closed after claude exits.
func (s *Session) run(ctx context.Context, args []string, prompt string) (<-chan Event, error) {
	cmd := exec.CommandContext(ctx, s.binary, args...)
	if s.cwd != "" {
		cmd.Dir = s.cwd
	}
	if len(s.env) > 0 {
		cmd.Env = append(os.Environ(), s.env...)
	}
	cmd.Stdin = strings.NewReader(prompt)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("claude stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("claude stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start claude: %w", err)
	}

	out := make(chan Event, 32)
	tail := &stderrTail{}
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		drainStderr(stderr, tail)
	}()
	go s.streamStdout(cmd, stdout, out, tail, stderrDone)
	return out, nil
}

func (s *Session) streamStdout(cmd *exec.Cmd, stdout io.ReadCloser, out chan<- Event, tail *stderrTail, stderrDone <-chan struct{}) {
	defer close(out)

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)

	// Per-invocation deduper: Claude Code emits one stream-json line per
	// content-block kind (thinking / tool_use / text), all sharing one
	// message id and repeating the same `usage` block. Without dedup the
	// launcher's per-API-call sum over-reports ~2×.
	dedup := newUsageDeduper()

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		for _, ev := range dedup.filter(parseLine(line)) {
			if ev.SessionID != "" && s.sessionID == "" {
				s.sessionID = ev.SessionID
			}
			out <- ev
		}
	}
	if err := scanner.Err(); err != nil {
		out <- Event{Kind: EventError, Err: fmt.Errorf("stream stdout: %w", err)}
	}
	// Drain stderr to natural EOF *before* cmd.Wait — StderrPipe's docs
	// warn that Wait closes the parent fds after process exit, which
	// will yank the pipe out from under any in-flight reader and lose
	// the tail (the part with the actual error message).
	<-stderrDone
	if err := cmd.Wait(); err != nil {
		if t := tail.String(); t != "" {
			out <- Event{Kind: EventError, Err: fmt.Errorf("claude exited: %w (stderr: %s)", err, t)}
		} else {
			out <- Event{Kind: EventError, Err: fmt.Errorf("claude exited: %w", err)}
		}
	}
}

func drainStderr(r io.ReadCloser, tail *stderrTail) {
	defer func() { _ = r.Close() }()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		log.Debug("claude-stderr", "line", line)
		tail.Append(line)
	}
}

// stderrTail is a tiny ring buffer over claude's stderr output. We only
// need the *last* ~4 KB at exit time — anything older isn't useful in
// an error message and would only push the relevant tail out.
type stderrTail struct {
	mu  sync.Mutex
	buf []byte
}

func (t *stderrTail) Append(line string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.buf) > 0 {
		t.buf = append(t.buf, '\n')
	}
	t.buf = append(t.buf, line...)
	if len(t.buf) > stderrTailMax {
		t.buf = t.buf[len(t.buf)-stderrTailMax:]
	}
}

func (t *stderrTail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
