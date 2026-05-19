//go:build windows

package terminalruntime

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var ErrConPTYUnavailable = errors.New("TERMINAL_CONPTY_UNAVAILABLE: Windows ConPTY driver is not available")

type windowsTerminalProcess struct {
	mu          sync.Mutex
	console     windows.Handle
	process     windows.Handle
	pid         int
	inputWrite  windows.Handle
	outputRead  windows.Handle
	closed      bool
	waited      bool
	exitCode    int
	processName string
}

func defaultTerminalCommand() (string, []string) {
	return "cmd.exe", nil
}

func startPlatformTerminal(ctx context.Context, command string, args []string, cwd string, _ []string, cols, rows int) (terminalProcess, error) {
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	process, err := startWindowsConPTYProcess(ctx, command, args, cwd, cols, rows)
	if err != nil {
		return nil, err
	}
	return process, nil
}

func startWindowsConPTYProcess(ctx context.Context, command string, args []string, cwd string, cols, rows int) (*windowsTerminalProcess, error) {
	var inputRead windows.Handle
	var inputWrite windows.Handle
	var outputRead windows.Handle
	var outputWrite windows.Handle
	security := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), InheritHandle: 1}
	if err := windows.CreatePipe(&inputRead, &inputWrite, &security, 0); err != nil {
		return nil, err
	}
	if err := windows.CreatePipe(&outputRead, &outputWrite, &security, 0); err != nil {
		windows.CloseHandle(inputRead)
		windows.CloseHandle(inputWrite)
		return nil, err
	}
	// The parent process owns inputWrite/outputRead; ConPTY consumes the opposite ends.
	consoleSize := windows.Coord{X: int16(cols), Y: int16(rows)}
	var console windows.Handle
	if err := windows.CreatePseudoConsole(consoleSize, inputRead, outputWrite, 0, &console); err != nil {
		windows.CloseHandle(inputRead)
		windows.CloseHandle(inputWrite)
		windows.CloseHandle(outputRead)
		windows.CloseHandle(outputWrite)
		return nil, err
	}
	if err := windows.CloseHandle(inputRead); err != nil {
		windows.ClosePseudoConsole(console)
		windows.CloseHandle(inputWrite)
		windows.CloseHandle(outputRead)
		windows.CloseHandle(outputWrite)
		return nil, err
	}
	if err := windows.CloseHandle(outputWrite); err != nil {
		windows.ClosePseudoConsole(console)
		windows.CloseHandle(inputWrite)
		windows.CloseHandle(outputRead)
		return nil, err
	}

	attrList, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		windows.ClosePseudoConsole(console)
		windows.CloseHandle(inputWrite)
		windows.CloseHandle(outputRead)
		return nil, err
	}
	defer attrList.Delete()
	if err := attrList.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, unsafe.Pointer(&console), unsafe.Sizeof(console)); err != nil {
		windows.ClosePseudoConsole(console)
		windows.CloseHandle(inputWrite)
		windows.CloseHandle(outputRead)
		return nil, err
	}

	startup := &windows.StartupInfoEx{}
	startup.Cb = uint32(unsafe.Sizeof(*startup))
	startup.ProcThreadAttributeList = attrList.List()
	procInfo := &windows.ProcessInformation{}

	cmdLine := windows.EscapeArg(command)
	for _, arg := range args {
		cmdLine += " " + windows.EscapeArg(arg)
	}
	cmdLinePtr, err := windows.UTF16PtrFromString(cmdLine)
	if err != nil {
		windows.ClosePseudoConsole(console)
		windows.CloseHandle(inputWrite)
		windows.CloseHandle(outputRead)
		return nil, err
	}
	cwdPtr, err := windows.UTF16PtrFromString(cwd)
	if err != nil {
		windows.ClosePseudoConsole(console)
		windows.CloseHandle(inputWrite)
		windows.CloseHandle(outputRead)
		return nil, err
	}

	creationFlags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT)
	if err := windows.CreateProcess(nil, cmdLinePtr, nil, nil, false, creationFlags, nil, cwdPtr, &startup.StartupInfo, procInfo); err != nil {
		windows.ClosePseudoConsole(console)
		windows.CloseHandle(inputWrite)
		windows.CloseHandle(outputRead)
		return nil, err
	}
	_ = windows.CloseHandle(procInfo.Thread)

	process := &windowsTerminalProcess{
		console:     console,
		process:     procInfo.Process,
		pid:         int(procInfo.ProcessId),
		inputWrite:  inputWrite,
		outputRead:  outputRead,
		processName: command,
	}
	_ = ctx
	return process, nil
}

func (p *windowsTerminalProcess) Read(buf []byte) (int, error) {
	if len(buf) == 0 {
		return 0, nil
	}
	var done uint32
	err := windows.ReadFile(p.outputRead, buf, &done, nil)
	if err != nil {
		if err == windows.ERROR_BROKEN_PIPE || err == windows.ERROR_HANDLE_EOF {
			return int(done), io.EOF
		}
		return int(done), err
	}
	return int(done), nil
}

func (p *windowsTerminalProcess) Write(buf []byte) (int, error) {
	if len(buf) == 0 {
		return 0, nil
	}
	var done uint32
	err := windows.WriteFile(p.inputWrite, buf, &done, nil)
	if err != nil {
		if err == windows.ERROR_BROKEN_PIPE || err == windows.ERROR_HANDLE_EOF {
			return int(done), io.EOF
		}
		return int(done), err
	}
	return int(done), nil
}

func (p *windowsTerminalProcess) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	if p.inputWrite != 0 {
		_ = windows.CloseHandle(p.inputWrite)
		p.inputWrite = 0
	}
	if p.outputRead != 0 {
		_ = windows.CloseHandle(p.outputRead)
		p.outputRead = 0
	}
	if p.console != 0 {
		windows.ClosePseudoConsole(p.console)
		p.console = 0
	}
	return nil
}

func (p *windowsTerminalProcess) Resize(cols, rows int) error {
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	return windows.ResizePseudoConsole(p.console, windows.Coord{X: int16(cols), Y: int16(rows)})
}

func (p *windowsTerminalProcess) PID() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pid
}

func (p *windowsTerminalProcess) Wait() int {
	p.mu.Lock()
	process := p.process
	p.mu.Unlock()
	if process == 0 {
		return 0
	}
	event, err := windows.WaitForSingleObject(process, windows.INFINITE)
	if err != nil || event != windows.WAIT_OBJECT_0 {
		return 1
	}
	var exitCode uint32
	if err := windows.GetExitCodeProcess(process, &exitCode); err != nil {
		return 1
	}
	p.mu.Lock()
	p.waited = true
	p.exitCode = int(exitCode)
	if p.process != 0 {
		_ = windows.CloseHandle(p.process)
		p.process = 0
	}
	p.pid = 0
	p.mu.Unlock()
	return int(exitCode)
}

func (p *windowsTerminalProcess) KillTree(ctx context.Context) {
	pid := p.PID()
	if pid <= 0 {
		return
	}
	_ = exec.CommandContext(ctx, "taskkill", "/T", "/F", "/PID", windowsIntToString(pid)).Run()
	p.mu.Lock()
	process := p.process
	p.mu.Unlock()
	if process != 0 {
		_ = windows.TerminateProcess(process, 1)
	}
}

func (m *Manager) killProcessTree(ctx context.Context, pid int) {
	if pid <= 0 {
		return
	}
	_ = exec.CommandContext(ctx, "taskkill", "/T", "/F", "/PID", windowsIntToString(pid)).Run()
}

func signalTerminalResize(int) {}

func platformTerminalCapabilities() map[string]any {
	return map[string]any{
		"supported":        true,
		"directPty":        true,
		"windowsPty":       true,
		"runtime":          "conpty",
		"fallback":         false,
		"dtachSupported":   false,
		"dtachAvailable":   false,
		"detachedReattach": false,
		"orphanRecovery":   false,
	}
}

func windowsIntToString(value int) string {
	if value == 0 {
		return "0"
	}
	neg := value < 0
	if neg {
		value = -value
	}
	buf := make([]byte, 0, 12)
	for value > 0 {
		buf = append(buf, byte('0'+value%10))
		value /= 10
	}
	if neg {
		buf = append(buf, '-')
	}
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return string(buf)
}
