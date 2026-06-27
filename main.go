package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
	"unsafe"
)

const (
	appName      = "UV Printer Log Monitor"
	className    = "UVPrinterLogMonitorWindow"
	configName   = "printer_monitor_config.json"
	defaultDelay = 3
	defaultStale = 10
)

type Config struct {
	ProgramDir        string `json:"program_dir"`
	LogPath           string `json:"log_path"`
	ServerChanSendKey string `json:"serverchan_sendkey"`
	PushDelayMinutes  int    `json:"push_delay_minutes"`
	NoResponseMinutes int    `json:"no_response_minutes"`
	AutoStart         bool   `json:"auto_start"`
	WatchdogEnabled   bool   `json:"watchdog_enabled"`
	WatchdogAutoStart bool   `json:"watchdog_auto_start"`
}

type Alert struct {
	Kind        string
	Title       string
	Detail      string
	TaskID      string
	CreatedAt   time.Time
	DueAt       time.Time
	Sent        bool
	RepeatEvery time.Duration
	SendResult  string
}

type MonitorState struct {
	mu                     sync.Mutex
	cfg                    Config
	logPath                string
	status                 string
	statusKind             string
	currentTask            string
	currentTaskStart       time.Time
	lastLogLineTime        time.Time
	lastLogStamp           string
	lastLine               string
	lastPrintProgressTime  time.Time
	lastPrintProgressStamp string
	lastPrintProgressLine  string
	activeAlert            *Alert
	events                 []string
	lastAlertKey           string
	completedTaskID        string
	noRespTaskID           string
	version                int
}

var state = &MonitorState{
	status:     "准备中",
	statusKind: "idle",
	events:     make([]string, 0, 200),
}

var (
	hInstance          uintptr
	hMain              uintptr
	hStatus            uintptr
	hDetail            uintptr
	hProgramDir        uintptr
	hLogPath           uintptr
	hSendKey           uintptr
	hDelay             uintptr
	hNoResp            uintptr
	hAutoStart         uintptr
	hWatchdog          uintptr
	hWatchdogAutoStart uintptr
	hEvents            uintptr
	hSaveBtn           uintptr
	hConfirmBtn        uintptr
	hReloadBtn         uintptr
	hTestBtn           uintptr
	hBrowseBtn         uintptr
	hFont              uintptr
	configPath         string
	debugPath          string
	debugFile          *os.File
	debugMu            sync.Mutex
	unmatchedDebugMu   sync.Mutex
	lastUnmatchedDebug time.Time
	watchdogMu         sync.Mutex
	lastWatchdogCheck  time.Time
	lastWatchdogNotice time.Time
	lastWatchdogLaunch time.Time
	monitorWake        = make(chan struct{}, 1)
	buildVersion       = "1.1.0"
	uiVariant          = "classic"
)

const (
	WS_OVERLAPPED       = 0x00000000
	WS_CAPTION          = 0x00C00000
	WS_SYSMENU          = 0x00080000
	WS_THICKFRAME       = 0x00040000
	WS_MINIMIZEBOX      = 0x00020000
	WS_MAXIMIZEBOX      = 0x00010000
	WS_OVERLAPPEDWINDOW = WS_OVERLAPPED | WS_CAPTION | WS_SYSMENU | WS_THICKFRAME | WS_MINIMIZEBOX | WS_MAXIMIZEBOX
	WS_VISIBLE          = 0x10000000
	WS_CHILD            = 0x40000000
	WS_BORDER           = 0x00800000
	WS_VSCROLL          = 0x00200000
	ES_AUTOHSCROLL      = 0x0080
	ES_AUTOVSCROLL      = 0x0040
	ES_MULTILINE        = 0x0004
	ES_READONLY         = 0x0800
	ES_PASSWORD         = 0x0020
	BS_PUSHBUTTON       = 0x00000000
	BS_AUTOCHECKBOX     = 0x00000003
	BST_CHECKED         = 0x00000001
	SS_LEFT             = 0x00000000
	SS_CENTER           = 0x00000001
	CW_USEDEFAULT       = 0x80000000
	SW_SHOW             = 5
	WM_CREATE           = 0x0001
	WM_DESTROY          = 0x0002
	WM_CLOSE            = 0x0010
	WM_COMMAND          = 0x0111
	WM_TIMER            = 0x0113
	WM_SETFONT          = 0x0030
	WM_SETTEXT          = 0x000C
	WM_GETTEXT          = 0x000D
	WM_GETTEXTLENGTH    = 0x000E
	BM_GETCHECK         = 0x00F0
	BM_SETCHECK         = 0x00F1
	COLOR_WINDOW        = 5
	IDC_ARROW           = 32512
	DEFAULT_GUI_FONT    = 17
)

const (
	idSave    = 1001
	idConfirm = 1002
	idReload  = 1003
	idTest    = 1004
	idBrowse  = 1005
)

var (
	user32                  = syscall.NewLazyDLL("user32.dll")
	kernel32                = syscall.NewLazyDLL("kernel32.dll")
	gdi32                   = syscall.NewLazyDLL("gdi32.dll")
	shell32                 = syscall.NewLazyDLL("shell32.dll")
	ole32                   = syscall.NewLazyDLL("ole32.dll")
	procRegisterClassExW    = user32.NewProc("RegisterClassExW")
	procCreateWindowExW     = user32.NewProc("CreateWindowExW")
	procDefWindowProcW      = user32.NewProc("DefWindowProcW")
	procShowWindow          = user32.NewProc("ShowWindow")
	procUpdateWindow        = user32.NewProc("UpdateWindow")
	procGetMessageW         = user32.NewProc("GetMessageW")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procDispatchMessageW    = user32.NewProc("DispatchMessageW")
	procPostQuitMessage     = user32.NewProc("PostQuitMessage")
	procLoadCursorW         = user32.NewProc("LoadCursorW")
	procSetTimer            = user32.NewProc("SetTimer")
	procKillTimer           = user32.NewProc("KillTimer")
	procSendMessageW        = user32.NewProc("SendMessageW")
	procSetWindowTextW      = user32.NewProc("SetWindowTextW")
	procGetWindowTextW      = user32.NewProc("GetWindowTextW")
	procGetWindowTextLength = user32.NewProc("GetWindowTextLengthW")
	procMessageBoxW         = user32.NewProc("MessageBoxW")
	procGetModuleHandleW    = kernel32.NewProc("GetModuleHandleW")
	procMultiByteToWideChar = kernel32.NewProc("MultiByteToWideChar")
	procGetStockObject      = gdi32.NewProc("GetStockObject")
	procSHBrowseForFolderW  = shell32.NewProc("SHBrowseForFolderW")
	procSHGetPathFromIDList = shell32.NewProc("SHGetPathFromIDListW")
	procCoTaskMemFree       = ole32.NewProc("CoTaskMemFree")
	procCoInitializeEx      = ole32.NewProc("CoInitializeEx")
	procCoUninitialize      = ole32.NewProc("CoUninitialize")
)

type WndClassEx struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

type Point struct {
	X int32
	Y int32
}

type Msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      Point
}

type BrowseInfo struct {
	HwndOwner      uintptr
	PidlRoot       uintptr
	PszDisplayName *uint16
	LpszTitle      *uint16
	UlFlags        uint32
	Lpfn           uintptr
	LParam         uintptr
	IImage         int32
}

func main() {
	runtime.LockOSThread()
	procCoInitializeEx.Call(0, 0x2)
	defer procCoUninitialize.Call()
	configPath = filepath.Join(exeDir(), configName)
	debugPath = filepath.Join(exeDir(), "printer_monitor_debug.log")
	initDebugLog()
	defer closeDebugLog()
	debugLog("program start version=" + buildVersion)
	cfg := loadConfig()
	cfg = normalizeConfig(cfg)
	state.mu.Lock()
	state.cfg = cfg
	state.logPath = currentLogPath(cfg)
	if cfg.ProgramDir == "" {
		state.status = "请选择打印程序目录"
		state.statusKind = "idle"
	}
	state.mu.Unlock()
	state.addEvent("程序启动，版本 " + buildVersion)
	debugLogf("config loaded program_dir=%q log_path=%q push_delay=%d no_response=%d", cfg.ProgramDir, currentLogPath(cfg), cfg.PushDelayMinutes, cfg.NoResponseMinutes)

	go monitorLoop()
	runGUI()
}

func initDebugLog() {
	f, err := os.OpenFile(debugPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err == nil {
		debugFile = f
	}
}

func closeDebugLog() {
	debugMu.Lock()
	defer debugMu.Unlock()
	if debugFile != nil {
		_ = debugFile.Close()
		debugFile = nil
	}
}

func debugLog(msg string) {
	debugMu.Lock()
	defer debugMu.Unlock()
	if debugFile == nil {
		return
	}
	line := time.Now().Format("2006-01-02 15:04:05.000") + "  " + msg + "\r\n"
	_, _ = debugFile.WriteString(line)
}

func debugLogf(format string, args ...any) {
	debugLog(fmt.Sprintf(format, args...))
}

func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

func desktopDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, "Desktop")
}

func normalizeConfig(cfg Config) Config {
	cfg.ProgramDir = strings.TrimSpace(cfg.ProgramDir)
	cfg.LogPath = strings.TrimSpace(cfg.LogPath)
	if cfg.PushDelayMinutes <= 0 {
		cfg.PushDelayMinutes = defaultDelay
	}
	if cfg.NoResponseMinutes <= 0 {
		cfg.NoResponseMinutes = defaultStale
	}
	if cfg.WatchdogAutoStart {
		cfg.WatchdogEnabled = true
	}
	if cfg.ProgramDir == "" && cfg.LogPath != "" {
		cfg.ProgramDir = inferProgramDir(cfg.LogPath)
	}
	if cfg.ProgramDir != "" {
		cfg.LogPath = logPathForDate(cfg.ProgramDir, time.Now())
	}
	return cfg
}

func loadConfig() Config {
	var cfg Config
	data, err := os.ReadFile(configPath)
	if err == nil {
		_ = json.Unmarshal(data, &cfg)
	}
	return cfg
}

func saveConfig(cfg Config) error {
	cfg = normalizeConfig(cfg)
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, data, 0600)
}

func currentLogPath(cfg Config) string {
	if strings.TrimSpace(cfg.ProgramDir) != "" {
		return logPathForDate(cfg.ProgramDir, time.Now())
	}
	return strings.TrimSpace(cfg.LogPath)
}

func logPathForDate(programDir string, t time.Time) string {
	return filepath.Join(strings.TrimSpace(programDir), "Log", "main", "Log["+t.Format("2006_01_02")+"].txt")
}

func inferProgramDir(logPath string) string {
	clean := filepath.Clean(strings.TrimSpace(logPath))
	mainDir := filepath.Dir(clean)
	logDir := filepath.Dir(mainDir)
	if strings.EqualFold(filepath.Base(mainDir), "main") && strings.EqualFold(filepath.Base(logDir), "Log") {
		return filepath.Dir(logDir)
	}
	return ""
}

func monitorLoop() {
	var current string
	var offset int64
	var partial []byte
	var lastNoDirLog time.Time
	var lastMissingPath string
	var lastMissingLog time.Time
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	debugLog("monitor loop started")

	for {
		select {
		case <-ticker.C:
		case <-monitorWake:
		}

		cfg := state.getConfig()
		path := currentLogPath(cfg)
		if path == "" {
			state.setStatus("请选择打印程序目录", "idle")
			if time.Since(lastNoDirLog) > 10*time.Second {
				debugLog("waiting for program directory selection")
				lastNoDirLog = time.Now()
			}
			time.Sleep(time.Second)
			continue
		}

		if path != current {
			current = path
			offset = 0
			partial = nil
			debugLogf("switching monitored log path to %q", current)
			parseExistingLog(current)
			if info, err := os.Stat(current); err == nil {
				offset = info.Size()
				state.mu.Lock()
				state.logPath = current
				state.lastLogLineTime = info.ModTime()
				state.version++
				state.mu.Unlock()
				debugLogf("historical read complete path=%q size=%d mod=%s", current, offset, info.ModTime().Format(time.RFC3339))
			} else {
				debugLogf("stat after parse failed path=%q err=%v", current, err)
			}
		}

		info, err := os.Stat(current)
		if err != nil {
			state.setStatus("等待当天日志文件", "idle")
			if current != lastMissingPath || time.Since(lastMissingLog) > time.Minute {
				debugLogf("waiting for log file path=%q err=%v", current, err)
				lastMissingPath = current
				lastMissingLog = time.Now()
			}
			time.Sleep(time.Second)
			continue
		}
		if info.Size() < offset {
			offset = 0
			partial = nil
			state.addEvent("检测到日志文件被截断或轮转，重新读取新增内容")
			debugLogf("log truncated or rotated path=%q new_size=%d", current, info.Size())
		}
		if info.Size() > offset {
			data, readErr := readRange(current, offset)
			if readErr == nil {
				debugLogf("read new bytes path=%q offset=%d bytes=%d", current, offset, len(data))
				offset += int64(len(data))
				partial = consumeTailBytes(append(partial, data...), false)
			} else {
				state.addEvent("读取日志新增内容失败：" + readErr.Error())
				debugLogf("read new bytes failed path=%q offset=%d err=%v", current, offset, readErr)
			}
		}

		state.checkTimers()
		state.checkPrintExpWatchdog()
	}
}

func readRange(path string, offset int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	_, err = f.Seek(offset, io.SeekStart)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(f)
}

func parseExistingLog(path string) {
	f, err := os.Open(path)
	if err != nil {
		state.addEvent("等待日志文件：" + path)
		debugLogf("open historical log failed path=%q err=%v", path, err)
		return
	}
	defer f.Close()
	debugLogf("open historical log ok path=%q", path)

	state.mu.Lock()
	state.logPath = path
	state.status = "读取历史状态"
	state.statusKind = "idle"
	state.activeAlert = nil
	state.version++
	state.mu.Unlock()
	state.addEvent("开始监控日志：" + path)

	reader := bufio.NewReaderSize(f, 1024*1024)
	lineCount := 0
	var timeline historicalTimeline
	for {
		lineBytes, err := reader.ReadBytes('\n')
		if len(lineBytes) > 0 {
			lineCount++
			line := strings.TrimRight(decodeBytes(lineBytes), "\r\n")
			timeline.observe(line, lineCount)
			handleLogLine(line, true)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			state.addEvent("读取历史日志失败：" + err.Error())
			debugLogf("historical read error path=%q err=%v", path, err)
			break
		}
	}
	state.mu.Lock()
	statusAfterRead := state.status
	if timeline.lastCritical > timeline.lastStart && timeline.lastCritical > timeline.lastComplete {
		state.status = "异常"
		state.statusKind = "error"
		statusAfterRead = state.status
	} else if timeline.lastStart > timeline.lastComplete {
		if state.currentTask == "" {
			state.currentTask = "temp.prn"
		}
		state.status = "打印中"
		state.statusKind = "printing"
		statusAfterRead = state.status
	} else if timeline.lastComplete > 0 && timeline.lastComplete > timeline.lastCritical {
		state.status = "打印完成"
		state.statusKind = "complete"
		statusAfterRead = state.status
	} else if statusAfterRead == "读取历史状态" {
		state.status = "等待打印开始"
		state.statusKind = "idle"
		statusAfterRead = state.status
	}
	state.version++
	state.mu.Unlock()
	state.addEvent(fmt.Sprintf("历史日志读取完成：%d 行，当前状态：%s", lineCount, statusAfterRead))
	debugLogf("historical parse finished path=%q lines=%d final_status=%q last_start=%d last_complete=%d last_critical=%d start_line=%q complete_line=%q critical_line=%q", path, lineCount, statusAfterRead, timeline.lastStart, timeline.lastComplete, timeline.lastCritical, timeline.startLine, timeline.completeLine, timeline.criticalLine)
	if statusAfterRead == "等待打印开始" {
		debugLog("historical log exists but no print start/progress/complete/error signal was detected")
	}
}

func consumeTailBytes(buf []byte, historic bool) []byte {
	for {
		i := bytes.IndexByte(buf, '\n')
		if i < 0 {
			return buf
		}
		end := i + 1
		if end < len(buf) && buf[end] == 0 {
			end++
		}
		lineBytes := buf[:end]
		buf = buf[end:]
		line := strings.TrimRight(decodeBytes(lineBytes), "\r\n")
		handleLogLine(line, historic)
	}
}

func decodeBytes(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	data = alignTextBytes(data)
	if len(data) == 0 {
		return ""
	}
	if len(data) >= 2 {
		if data[0] == 0xff && data[1] == 0xfe {
			return decodeUTF16Bytes(data[2:], true)
		}
		if data[0] == 0xfe && data[1] == 0xff {
			return decodeUTF16Bytes(data[2:], false)
		}
	}
	if looksUTF16(data, true) {
		return decodeUTF16Bytes(data, true)
	}
	if looksUTF16(data, false) {
		return decodeUTF16Bytes(data, false)
	}
	if utf8.Valid(data) {
		return string(data)
	}
	return decodeCodePage(936, data)
}

func alignTextBytes(data []byte) []byte {
	for len(data) > 0 && data[0] == 0 {
		data = data[1:]
	}
	return data
}

func looksUTF16(data []byte, little bool) bool {
	if len(data) < 4 {
		return false
	}
	pairs := len(data) / 2
	if pairs == 0 {
		return false
	}
	zeroSide := 1
	textSide := 0
	if !little {
		zeroSide = 0
		textSide = 1
	}
	zeroCount := 0
	textCount := 0
	otherZeroCount := 0
	for i := 0; i+1 < len(data); i += 2 {
		if data[i+zeroSide] == 0 {
			zeroCount++
		}
		if data[i+textSide] >= 0x20 || data[i+textSide] == '\r' || data[i+textSide] == '\n' || data[i+textSide] == '\t' {
			textCount++
		}
		if data[i+textSide] == 0 {
			otherZeroCount++
		}
	}
	return zeroCount >= 2 && zeroCount*2 >= pairs && textCount*2 >= pairs && zeroCount > otherZeroCount
}

func decodeUTF16Bytes(data []byte, little bool) string {
	if len(data) == 0 {
		return ""
	}
	if len(data)%2 != 0 {
		copied := make([]byte, len(data)+1)
		copy(copied, data)
		data = copied
	}
	u16 := make([]uint16, 0, len(data)/2)
	for i := 0; i+1 < len(data); i += 2 {
		var v uint16
		if little {
			v = uint16(data[i]) | uint16(data[i+1])<<8
		} else {
			v = uint16(data[i])<<8 | uint16(data[i+1])
		}
		u16 = append(u16, v)
	}
	return syscall.UTF16ToString(u16)
}

func decodeCodePage(cp uint32, data []byte) string {
	if len(data) == 0 {
		return ""
	}
	r1, _, _ := procMultiByteToWideChar.Call(
		uintptr(cp),
		0,
		uintptr(unsafe.Pointer(&data[0])),
		uintptr(len(data)),
		0,
		0,
	)
	if r1 == 0 {
		return string(data)
	}
	buf := make([]uint16, r1)
	r2, _, _ := procMultiByteToWideChar.Call(
		uintptr(cp),
		0,
		uintptr(unsafe.Pointer(&data[0])),
		uintptr(len(data)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
	)
	if r2 == 0 {
		return string(data)
	}
	return syscall.UTF16ToString(buf)
}

var (
	logTimeRE     = regexp.MustCompile(`^\[(\d\d:\d\d:\d\d(?:[.:]\d+)?)\]`)
	logPathDateRE = regexp.MustCompile(`Log\[(\d{4})_(\d{2})_(\d{2})\]`)
)

type historicalTimeline struct {
	lastStart    int
	lastComplete int
	lastCritical int
	startLine    string
	completeLine string
	criticalLine string
}

func (h *historicalTimeline) observe(line string, seq int) {
	lower := strings.ToLower(line)
	if isPrintEvidenceSignal(line) {
		h.lastStart = seq
		h.startLine = line
	}
	if isPrintCompleteSignal(line) {
		h.lastComplete = seq
		h.completeLine = line
	}
	if isCriticalLine(line, lower) {
		h.lastCritical = seq
		h.criticalLine = line
	}
}

func isPrintStartSignal(line string) bool {
	return strings.Contains(line, "启动任务：") ||
		strings.Contains(line, "StartPrint Begin") ||
		strings.Contains(line, "板卡数据复位完成，进入打印动作") ||
		strings.Contains(line, "PRINT_STATUS_PRINT") ||
		strings.Contains(line, "等待下位机进入打印状态") ||
		strings.Contains(line, "打印控制线程---设置发送数据标志为真") ||
		strings.Contains(line, "Start Write PassInfo") ||
		strings.Contains(line, "nCurPass=") ||
		strings.Contains(line, "OnRipOutputData") ||
		strings.Contains(line, "OnRipGetRipSourceData")
}

func isPrintProgressSignal(line string) bool {
	return strings.Contains(line, "启动任务：") ||
		strings.Contains(line, "StartPrint Begin") ||
		strings.Contains(line, "板卡数据复位完成，进入打印动作") ||
		strings.Contains(line, "PRINT_STATUS_PRINT") ||
		strings.Contains(line, "打印控制线程---设置发送数据标志为真") ||
		strings.Contains(line, "Start Write PassInfo") ||
		strings.Contains(line, "nCurPass=") ||
		strings.Contains(line, "OnRipOutputData") ||
		strings.Contains(line, "OnRipGetRipSourceData") ||
		strings.Contains(line, "sRip.nIndex=") ||
		strings.Contains(line, "sRip.nSectionWidth") ||
		strings.Contains(line, "ShowSegContour") ||
		strings.Contains(line, "nPassId:") ||
		strings.Contains(line, "nOffsetY:") ||
		strings.Contains(line, "动态贴图耗时") ||
		strings.Contains(line, "成功识别") ||
		(strings.Contains(line, "正在打印") && strings.Contains(line, "进度"))
}

func isPrintEvidenceSignal(line string) bool {
	return isPrintStartSignal(line) || isPrintProgressSignal(line)
}

func isPrintCompleteSignal(line string) bool {
	return strings.Contains(line, "_PrintWait---打印完成") ||
		strings.Contains(line, "打印完成................................")
}

func handleLogLine(line string, historic bool) {
	if strings.TrimSpace(line) == "" {
		return
	}
	stamp := extractLogStamp(line)
	now := time.Now()

	state.mu.Lock()
	state.lastLine = line
	state.lastLogStamp = stamp
	state.lastLogLineTime = now
	if !historic {
		state.version++
	}
	state.mu.Unlock()

	lower := strings.ToLower(line)
	printEvidence := isPrintEvidenceSignal(line)
	printProgress := isPrintProgressSignal(line)
	printComplete := isPrintCompleteSignal(line)
	critical := isCriticalLine(line, lower)
	if strings.Contains(line, "启动任务：") {
		task := after(line, "启动任务：")
		if task == "" {
			task = "打印任务"
		}
		state.startTask(task, stamp, historic)
	}
	if printEvidence {
		state.markPrinting(stamp, line, historic)
	}
	if printProgress {
		state.markPrintProgress(stamp, line, historic)
	}
	if strings.Contains(line, "相机SDK--扫描") || strings.Contains(line, "imgMosaicAdd return") {
		state.markActivity("扫描/图像处理中", stamp, historic)
	}
	if printComplete {
		state.markComplete(stamp, line, historic)
	}
	if critical {
		state.markError(stamp, line, historic)
	}
	if !historic && !printEvidence && !printComplete && !critical {
		debugUnclassifiedLine(stamp, line)
	}
}

func debugUnclassifiedLine(stamp, line string) {
	now := time.Now()
	unmatchedDebugMu.Lock()
	defer unmatchedDebugMu.Unlock()
	if now.Sub(lastUnmatchedDebug) < 30*time.Second {
		return
	}
	lastUnmatchedDebug = now
	debugLogf("live log sample unclassified stamp=%q line=%q", stamp, line)
}

func extractLogStamp(line string) string {
	m := logTimeRE.FindStringSubmatch(line)
	if len(m) >= 2 {
		return m[1]
	}
	return time.Now().Format("15:04:05")
}

func stampTimeForPath(stamp, path string, fallback time.Time) time.Time {
	if stamp == "" {
		return fallback
	}
	normalized := strings.Replace(stamp, ".", ":", 1)
	layouts := []string{"15:04:05:000", "15:04:05"}
	var tod time.Time
	var err error
	for _, layout := range layouts {
		tod, err = time.ParseInLocation(layout, normalized, time.Local)
		if err == nil {
			break
		}
	}
	if err != nil {
		return fallback
	}

	year, month, day := fallback.Date()
	if m := logPathDateRE.FindStringSubmatch(path); len(m) == 4 {
		if y, yErr := strconv.Atoi(m[1]); yErr == nil {
			if mo, moErr := strconv.Atoi(m[2]); moErr == nil {
				if d, dErr := strconv.Atoi(m[3]); dErr == nil {
					year, month, day = y, time.Month(mo), d
				}
			}
		}
	}
	return time.Date(year, month, day, tod.Hour(), tod.Minute(), tod.Second(), tod.Nanosecond(), time.Local)
}

func after(s, marker string) string {
	i := strings.Index(s, marker)
	if i < 0 {
		return ""
	}
	return strings.TrimSpace(s[i+len(marker):])
}

func isCriticalLine(line, lower string) bool {
	if strings.Contains(line, "[错误]") {
		return true
	}
	patterns := []string{
		"系统异常",
		"防撞触发",
		"急停",
		"设备断开",
		"特征生成失败",
		"运动X到扫描起始位置 失败",
		"已触发防撞",
		"不允许马达",
		"fatal",
		"exception",
	}
	for _, p := range patterns {
		if strings.Contains(line, p) || strings.Contains(lower, strings.ToLower(p)) {
			return true
		}
	}
	return false
}

func isEmergencyLine(line, lower string) bool {
	patterns := []string{
		"撞头",
		"防撞",
		"防撞触发",
		"已触发防撞",
		"急停",
		"emergency stop",
		"collision",
	}
	for _, p := range patterns {
		if strings.Contains(line, p) || strings.Contains(lower, strings.ToLower(p)) {
			return true
		}
	}
	return false
}

func (s *MonitorState) getConfig() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

func (s *MonitorState) setConfig(cfg Config) {
	cfg = normalizeConfig(cfg)
	s.mu.Lock()
	s.cfg = cfg
	s.logPath = currentLogPath(cfg)
	s.version++
	s.mu.Unlock()
	s.addEvent("配置已更新")
	debugLogf("config updated program_dir=%q log_path=%q push_delay=%d no_response=%d", cfg.ProgramDir, currentLogPath(cfg), cfg.PushDelayMinutes, cfg.NoResponseMinutes)
	select {
	case monitorWake <- struct{}{}:
	default:
	}
}

func (s *MonitorState) setStatus(status, kind string) {
	s.mu.Lock()
	changed := s.status != status || s.statusKind != kind
	if changed {
		s.status = status
		s.statusKind = kind
		s.version++
	}
	s.mu.Unlock()
	if changed {
		debugLogf("status changed status=%q kind=%q", status, kind)
	}
}

func (s *MonitorState) startTask(task, stamp string, historic bool) {
	s.mu.Lock()
	if s.currentTask == "" || !strings.Contains(s.status, "打印中") {
		s.currentTaskStart = time.Now()
	}
	s.currentTask = strings.TrimSpace(task)
	if s.currentTask == "" {
		s.currentTask = "temp.prn"
	}
	s.status = "任务已启动"
	s.statusKind = "printing"
	s.currentTaskStart = time.Now()
	if historic && stamp != "" {
		s.currentTaskStart = time.Now()
	}
	s.completedTaskID = ""
	s.noRespTaskID = ""
	s.version++
	s.mu.Unlock()
	if !historic {
		s.addEvent("任务启动：" + s.currentTask)
	}
	if !historic {
		debugLogf("task started task=%q stamp=%q", s.currentTask, stamp)
	}
}

func (s *MonitorState) markPrinting(stamp, line string, historic bool) {
	s.mu.Lock()
	if s.currentTask == "" {
		s.currentTask = "temp.prn"
		s.currentTaskStart = time.Now()
	}
	s.status = "打印中"
	s.statusKind = "printing"
	s.version++
	s.mu.Unlock()
	if !historic && (strings.Contains(line, "PRINT_STATUS_PRINT") || strings.Contains(line, "进入打印动作")) {
		s.addEvent("进入打印状态：" + stamp)
		debugLogf("printing detected stamp=%q line=%q", stamp, line)
	}
}

func (s *MonitorState) markPrintProgress(stamp, line string, historic bool) {
	now := time.Now()
	s.mu.Lock()
	progressAt := now
	if historic {
		progressAt = stampTimeForPath(stamp, s.logPath, now)
	}
	s.lastPrintProgressTime = progressAt
	s.lastPrintProgressStamp = stamp
	s.lastPrintProgressLine = line
	s.version++
	s.mu.Unlock()
	if !historic {
		debugLogf("print progress updated stamp=%q at=%s line=%q", stamp, progressAt.Format(time.RFC3339), line)
	}
}

func (s *MonitorState) markActivity(status, stamp string, historic bool) {
	s.mu.Lock()
	if s.statusKind != "printing" && s.activeAlert == nil {
		s.status = status
		s.statusKind = "idle"
		s.version++
	}
	s.mu.Unlock()
}

func (s *MonitorState) markComplete(stamp, line string, historic bool) {
	taskID := s.taskID()
	s.mu.Lock()
	s.status = "打印完成"
	s.statusKind = "complete"
	s.noRespTaskID = ""
	already := s.completedTaskID == taskID && taskID != ""
	if !already {
		s.completedTaskID = taskID
	}
	s.version++
	s.mu.Unlock()
	if !historic && !already {
		s.addEvent("打印完成：" + stamp)
		debugLogf("print completed stamp=%q line=%q", stamp, line)
		s.triggerAlert("complete", "打印完成", "日志记录打印任务已完成。\r\n\r\n"+line)
	}
}

func (s *MonitorState) markError(stamp, line string, historic bool) {
	lower := strings.ToLower(line)
	kind := "error"
	title := "打印异常"
	if isEmergencyLine(line, lower) {
		kind = "emergency"
		title = "撞头/急停触发"
	}
	s.mu.Lock()
	s.status = title
	s.statusKind = kind
	s.version++
	s.mu.Unlock()
	if !historic {
		s.addEvent(title + "：" + line)
		debugLogf("critical error detected kind=%q stamp=%q line=%q", kind, stamp, line)
		s.triggerAlert(kind, title, line)
	}
}

func (s *MonitorState) taskID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.currentTaskStart.IsZero() {
		return s.currentTask + "@" + s.currentTaskStart.Format(time.RFC3339Nano)
	}
	return s.currentTask
}

func (s *MonitorState) triggerAlert(kind, title, detail string) {
	cfg := s.getConfig()
	key := kind + "|" + s.taskID() + "|" + detail
	s.mu.Lock()
	if s.lastAlertKey == key {
		s.mu.Unlock()
		return
	}
	s.lastAlertKey = key
	delay, repeatEvery := alertSchedule(kind, cfg)
	now := time.Now()
	alert := &Alert{
		Kind:        kind,
		Title:       title,
		Detail:      detail,
		TaskID:      s.currentTask,
		CreatedAt:   now,
		DueAt:       now.Add(delay),
		RepeatEvery: repeatEvery,
	}
	s.activeAlert = alert
	s.status = title + "，待确认"
	s.statusKind = kind
	s.version++
	s.mu.Unlock()
	s.addEvent(fmt.Sprintf("%s：%s 后推送 Server酱", title, formatDuration(delay)))
	debugLogf("alert created kind=%q title=%q due=%s repeat_every=%s detail=%q", kind, title, alert.DueAt.Format(time.RFC3339), repeatEvery, detail)
}

func alertSchedule(kind string, cfg Config) (time.Duration, time.Duration) {
	switch kind {
	case "complete":
		return 0, 0
	case "emergency":
		return 0, 2 * time.Minute
	default:
		delay := time.Duration(cfg.PushDelayMinutes) * time.Minute
		if delay <= 0 {
			delay = defaultDelay * time.Minute
		}
		return delay, 0
	}
}

func (s *MonitorState) confirmAlert() {
	s.mu.Lock()
	if s.activeAlert == nil {
		s.mu.Unlock()
		s.addEvent("当前没有待确认提醒")
		return
	}
	title := s.activeAlert.Title
	s.activeAlert = nil
	if s.statusKind == "complete" || strings.Contains(s.status, "完成") {
		s.status = "已确认完成"
		s.statusKind = "idle"
	} else {
		s.status = "已确认"
		s.statusKind = "idle"
	}
	s.version++
	s.mu.Unlock()
	s.addEvent("已确认：" + title)
	debugLog("alert confirmed: " + title)
}

func (s *MonitorState) checkTimers() {
	now := time.Now()
	cfg := s.getConfig()
	var alertToSend *Alert

	s.mu.Lock()
	if (s.statusKind == "printing" || strings.Contains(s.status, "打印中")) && s.activeAlert == nil {
		staleMinutes := cfg.NoResponseMinutes
		if staleMinutes <= 0 {
			staleMinutes = defaultStale
		}
		progressTime := s.lastPrintProgressTime
		progressStamp := s.lastPrintProgressStamp
		progressLine := s.lastPrintProgressLine
		if progressTime.IsZero() {
			progressTime = s.currentTaskStart
			progressStamp = "任务开始"
			progressLine = s.currentTask
		}
		if progressTime.IsZero() {
			progressTime = s.lastLogLineTime
			progressStamp = s.lastLogStamp
			progressLine = s.lastLine
		}
		if !progressTime.IsZero() && now.Sub(progressTime) > time.Duration(staleMinutes)*time.Minute {
			taskID := s.currentTask + "@" + s.currentTaskStart.Format(time.RFC3339Nano)
			if s.noRespTaskID != taskID {
				s.noRespTaskID = taskID
				lastLogStamp := s.lastLogStamp
				lastLine := s.lastLine
				s.mu.Unlock()
				debugLogf("no response detected stale_minutes=%d last_progress_at=%s last_progress_stamp=%q last_progress_line=%q last_log_stamp=%q last_log_line=%q", staleMinutes, progressTime.Format(time.RFC3339), progressStamp, progressLine, lastLogStamp, lastLine)
				s.triggerAlert("noresponse", "长时间无响应", fmt.Sprintf("打印进度超过 %d 分钟没有更新。\r\n最后打印进度时间：%s\r\n最后打印进度：%s\r\n最后日志时间：%s\r\n最后日志：%s", staleMinutes, progressStamp, progressLine, lastLogStamp, lastLine))
				return
			}
		}
	}
	if s.activeAlert != nil && (!s.activeAlert.Sent || s.activeAlert.RepeatEvery > 0) && now.After(s.activeAlert.DueAt) {
		copyAlert := *s.activeAlert
		alertToSend = &copyAlert
		s.activeAlert.Sent = true
		if s.activeAlert.RepeatEvery > 0 {
			s.activeAlert.DueAt = now.Add(s.activeAlert.RepeatEvery)
		}
		s.version++
	}
	s.mu.Unlock()

	if alertToSend != nil {
		debugLogf("sending serverchan alert title=%q kind=%q", alertToSend.Title, alertToSend.Kind)
		result := sendServerChan(cfg, alertToSend)
		s.mu.Lock()
		if s.activeAlert != nil && s.activeAlert.CreatedAt.Equal(alertToSend.CreatedAt) {
			s.activeAlert.SendResult = result
		}
		s.version++
		s.mu.Unlock()
		s.addEvent("Server酱推送结果：" + result)
		debugLog("serverchan result: " + result)
	}
}

func sendServerChan(cfg Config, alert *Alert) string {
	key := strings.TrimSpace(cfg.ServerChanSendKey)
	if key == "" {
		return "未配置 SendKey"
	}
	endpoint := key
	if !strings.HasPrefix(strings.ToLower(endpoint), "http://") && !strings.HasPrefix(strings.ToLower(endpoint), "https://") {
		endpoint = "https://sctapi.ftqq.com/" + url.PathEscape(key) + ".send"
	}
	title := "[UV打印机] " + alert.Title
	desp := buildPushDesp(alert)
	values := url.Values{}
	values.Set("title", title)
	values.Set("desp", desp)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.PostForm(endpoint, values)
	if err != nil {
		return "失败：" + err.Error()
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Sprintf("HTTP %d：%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return strings.TrimSpace(string(body))
}

func (s *MonitorState) checkPrintExpWatchdog() {
	cfg := s.getConfig()
	if !cfg.WatchdogEnabled {
		return
	}
	now := time.Now()
	watchdogMu.Lock()
	if now.Sub(lastWatchdogCheck) < 15*time.Second {
		watchdogMu.Unlock()
		return
	}
	lastWatchdogCheck = now
	watchdogMu.Unlock()

	running, err := isProcessRunning("PrintExp.exe")
	if err != nil {
		debugLogf("watchdog process check failed err=%v", err)
	}
	if running {
		return
	}

	notice := func(msg string) {
		watchdogMu.Lock()
		should := now.Sub(lastWatchdogNotice) >= time.Minute
		if should {
			lastWatchdogNotice = now
		}
		watchdogMu.Unlock()
		if should {
			s.addEvent(msg)
			debugLog(msg)
		}
	}

	if !cfg.WatchdogAutoStart {
		notice("守护：PrintExp.exe 未运行")
		return
	}

	watchdogMu.Lock()
	if now.Sub(lastWatchdogLaunch) < 2*time.Minute {
		watchdogMu.Unlock()
		return
	}
	lastWatchdogLaunch = now
	watchdogMu.Unlock()

	exePath := findPrintExpExe(cfg.ProgramDir)
	if exePath == "" {
		notice("守护：PrintExp.exe 未运行，且未在打印程序目录找到 PrintExp.exe")
		return
	}
	cmd := exec.Command(exePath)
	cmd.Dir = filepath.Dir(exePath)
	if err := cmd.Start(); err != nil {
		notice("守护：尝试启动 PrintExp.exe 失败：" + err.Error())
		debugLogf("watchdog launch failed path=%q err=%v", exePath, err)
		return
	}
	s.addEvent("守护：已尝试启动 PrintExp.exe")
	debugLogf("watchdog launched PrintExp path=%q pid=%d", exePath, cmd.Process.Pid)
}

func isProcessRunning(imageName string) (bool, error) {
	out, err := exec.Command("tasklist", "/FI", "IMAGENAME eq "+imageName, "/NH").CombinedOutput()
	if err != nil {
		return false, err
	}
	return strings.Contains(strings.ToLower(string(out)), strings.ToLower(imageName)), nil
}

func findPrintExpExe(programDir string) string {
	programDir = strings.TrimSpace(programDir)
	if programDir == "" {
		return ""
	}
	candidate := filepath.Join(programDir, "PrintExp.exe")
	if fileExists(candidate) {
		return candidate
	}
	var found string
	_ = filepath.WalkDir(programDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || found != "" {
			return nil
		}
		if d.IsDir() {
			if strings.EqualFold(d.Name(), "Log") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.EqualFold(d.Name(), "PrintExp.exe") {
			found = path
		}
		return nil
	})
	return found
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func applyAutoStartSetting(enabled bool) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	runKey := `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
	valueName := "UVPrinterLogMonitor"
	if enabled {
		value := `"` + exe + `"`
		return exec.Command("reg", "add", runKey, "/v", valueName, "/t", "REG_SZ", "/d", value, "/f").Run()
	}
	err = exec.Command("reg", "delete", runKey, "/v", valueName, "/f").Run()
	if err != nil {
		debugLogf("autostart delete returned err=%v", err)
	}
	return nil
}

func buildPushDesp(alert *Alert) string {
	state.mu.Lock()
	defer state.mu.Unlock()
	lines := []string{
		"### " + alert.Title,
		"",
		"- 当前任务：" + valueOr(state.currentTask, "未知"),
		"- 当前状态：" + state.status,
		"- 日志文件：" + state.logPath,
		"- 最后日志时间：" + valueOr(state.lastLogStamp, "未知"),
		"- 触发时间：" + alert.CreatedAt.Format("2006-01-02 15:04:05"),
		"",
		"详情：",
		"",
		"```",
		alert.Detail,
		"```",
	}
	return strings.Join(lines, "\n")
}

func valueOr(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func (s *MonitorState) addEvent(msg string) {
	debugLog("event: " + msg)
	s.mu.Lock()
	defer s.mu.Unlock()
	line := time.Now().Format("15:04:05") + "  " + msg
	s.events = append(s.events, line)
	if len(s.events) > 300 {
		s.events = s.events[len(s.events)-300:]
	}
	s.version++
}

func runGUI() {
	r1, _, _ := procGetModuleHandleW.Call(0)
	hInstance = r1
	hFont, _, _ = procGetStockObject.Call(DEFAULT_GUI_FONT)
	cursor, _, _ := procLoadCursorW.Call(0, IDC_ARROW)
	class := utf16Ptr(className)
	wc := WndClassEx{
		CbSize:        uint32(unsafe.Sizeof(WndClassEx{})),
		LpfnWndProc:   syscall.NewCallback(wndProc),
		HInstance:     hInstance,
		HCursor:       cursor,
		HbrBackground: uintptr(COLOR_WINDOW + 1),
		LpszClassName: class,
	}
	procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

	width, height := windowSize()
	title := utf16Ptr(windowTitle())
	hwnd, _, _ := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(class)),
		uintptr(unsafe.Pointer(title)),
		WS_OVERLAPPEDWINDOW|WS_VISIBLE,
		CW_USEDEFAULT,
		CW_USEDEFAULT,
		uintptr(width),
		uintptr(height),
		0,
		0,
		hInstance,
		0,
	)
	hMain = hwnd
	procShowWindow.Call(hwnd, SW_SHOW)
	procUpdateWindow.Call(hwnd)

	var msg Msg
	for {
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(ret) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

func normalizedUIVariant() string {
	return "classic"
}

func windowTitle() string {
	return "UV打印机日志监控 Stable @kyroslee 2026 Jun"
}

func windowSize() (int, int) {
	return 780, 640
}

func wndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case WM_CREATE:
		createControls(hwnd)
		loadConfigToControls()
		procSetTimer.Call(hwnd, 1, 1000, 0)
		updateUI()
		return 0
	case WM_COMMAND:
		id := int(wParam & 0xffff)
		switch id {
		case idBrowse:
			if dir := chooseFolder(hwnd); dir != "" {
				setText(hProgramDir, dir)
				cfg := readConfigFromControls()
				cfg = normalizeConfig(cfg)
				setText(hLogPath, currentLogPath(cfg))
				if err := saveConfig(cfg); err != nil {
					state.addEvent("目录已选择，但保存配置失败：" + err.Error())
					debugLogf("selected program dir save failed dir=%q err=%v", dir, err)
				} else {
					state.setConfig(cfg)
					loadConfigToControls()
					state.addEvent("已选择打印程序目录：" + dir)
					debugLogf("selected program dir applied dir=%q log_path=%q", dir, currentLogPath(cfg))
				}
			}
		case idSave:
			saveConfigFromControls()
		case idConfirm:
			state.confirmAlert()
			updateUI()
		case idReload:
			saveConfigFromControls()
			select {
			case monitorWake <- struct{}{}:
			default:
			}
		case idTest:
			testPush()
		}
		return 0
	case WM_TIMER:
		updateUI()
		return 0
	case WM_CLOSE:
		procKillTimer.Call(hwnd, 1)
		procPostQuitMessage.Call(0)
		return 0
	case WM_DESTROY:
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
	return r
}

func createControls(hwnd uintptr) {
	createClassicControls(hwnd)
}

func applyFont(handles ...uintptr) {
	for _, h := range handles {
		if h != 0 {
			procSendMessageW.Call(h, WM_SETFONT, hFont, 1)
		}
	}
}

func createClassicControls(hwnd uintptr) {
	hStatus = makeControl("STATIC", "状态：准备中", WS_CHILD|WS_VISIBLE|SS_CENTER, 20, 18, 730, 38, hwnd, 0, 0)
	hDetail = makeControl("STATIC", "", WS_CHILD|WS_VISIBLE|SS_LEFT, 20, 60, 730, 70, hwnd, 0, 0)

	makeControl("STATIC", "打印程序目录", WS_CHILD|WS_VISIBLE|SS_LEFT, 20, 145, 95, 24, hwnd, 0, 0)
	hProgramDir = makeControl("EDIT", "", WS_CHILD|WS_VISIBLE|WS_BORDER|ES_AUTOHSCROLL, 120, 142, 525, 24, hwnd, 0, 0)
	hBrowseBtn = makeControl("BUTTON", "更换目录", WS_CHILD|WS_VISIBLE|BS_PUSHBUTTON, 660, 140, 90, 28, hwnd, idBrowse, 0)

	makeControl("STATIC", "当前日志", WS_CHILD|WS_VISIBLE|SS_LEFT, 20, 182, 80, 24, hwnd, 0, 0)
	hLogPath = makeControl("EDIT", "", WS_CHILD|WS_VISIBLE|WS_BORDER|ES_AUTOHSCROLL|ES_READONLY, 120, 179, 630, 24, hwnd, 0, 0)

	makeControl("STATIC", "Server酱 SendKey 或完整 send URL", WS_CHILD|WS_VISIBLE|SS_LEFT, 20, 219, 230, 24, hwnd, 0, 0)
	hSendKey = makeControl("EDIT", "", WS_CHILD|WS_VISIBLE|WS_BORDER|ES_AUTOHSCROLL|ES_PASSWORD, 255, 216, 495, 24, hwnd, 0, 0)

	makeControl("STATIC", "推送等待(分钟)", WS_CHILD|WS_VISIBLE|SS_LEFT, 20, 256, 110, 24, hwnd, 0, 0)
	hDelay = makeControl("EDIT", "", WS_CHILD|WS_VISIBLE|WS_BORDER|ES_AUTOHSCROLL, 135, 253, 70, 24, hwnd, 0, 0)
	makeControl("STATIC", "无响应阈值(分钟)", WS_CHILD|WS_VISIBLE|SS_LEFT, 230, 256, 130, 24, hwnd, 0, 0)
	hNoResp = makeControl("EDIT", "", WS_CHILD|WS_VISIBLE|WS_BORDER|ES_AUTOHSCROLL, 365, 253, 70, 24, hwnd, 0, 0)

	hSaveBtn = makeControl("BUTTON", "保存配置", WS_CHILD|WS_VISIBLE|BS_PUSHBUTTON, 460, 251, 90, 28, hwnd, idSave, 0)
	hReloadBtn = makeControl("BUTTON", "重新读取", WS_CHILD|WS_VISIBLE|BS_PUSHBUTTON, 560, 251, 90, 28, hwnd, idReload, 0)
	hTestBtn = makeControl("BUTTON", "测试推送", WS_CHILD|WS_VISIBLE|BS_PUSHBUTTON, 660, 251, 90, 28, hwnd, idTest, 0)
	hAutoStart = makeControl("BUTTON", "开机自启动", WS_CHILD|WS_VISIBLE|BS_AUTOCHECKBOX, 20, 289, 115, 24, hwnd, 0, 0)
	hWatchdog = makeControl("BUTTON", "守护 PrintExp", WS_CHILD|WS_VISIBLE|BS_AUTOCHECKBOX, 160, 289, 125, 24, hwnd, 0, 0)
	hWatchdogAutoStart = makeControl("BUTTON", "PrintExp 未运行时自动启动", WS_CHILD|WS_VISIBLE|BS_AUTOCHECKBOX, 310, 289, 210, 24, hwnd, 0, 0)

	hConfirmBtn = makeControl("BUTTON", "确认当前提醒", WS_CHILD|WS_VISIBLE|BS_PUSHBUTTON, 20, 320, 730, 34, hwnd, idConfirm, 0)

	makeControl("STATIC", "事件记录", WS_CHILD|WS_VISIBLE|SS_LEFT, 20, 368, 80, 24, hwnd, 0, 0)
	hEvents = makeControl("EDIT", "", WS_CHILD|WS_VISIBLE|WS_BORDER|WS_VSCROLL|ES_MULTILINE|ES_AUTOVSCROLL|ES_READONLY, 20, 394, 730, 180, hwnd, 0, 0)

	applyFont(hStatus, hDetail, hProgramDir, hBrowseBtn, hLogPath, hSendKey, hDelay, hNoResp, hAutoStart, hWatchdog, hWatchdogAutoStart, hSaveBtn, hReloadBtn, hTestBtn, hConfirmBtn, hEvents)
}

func makeControl(class, text string, style uintptr, x, y, w, h int, parent uintptr, id int, exStyle uintptr) uintptr {
	c := utf16Ptr(class)
	t := utf16Ptr(text)
	hwnd, _, _ := procCreateWindowExW.Call(
		exStyle,
		uintptr(unsafe.Pointer(c)),
		uintptr(unsafe.Pointer(t)),
		style,
		uintptr(x),
		uintptr(y),
		uintptr(w),
		uintptr(h),
		parent,
		uintptr(id),
		hInstance,
		0,
	)
	return hwnd
}

func loadConfigToControls() {
	cfg := state.getConfig()
	setText(hProgramDir, cfg.ProgramDir)
	setText(hLogPath, currentLogPath(cfg))
	setText(hSendKey, cfg.ServerChanSendKey)
	setText(hDelay, strconv.Itoa(cfg.PushDelayMinutes))
	setText(hNoResp, strconv.Itoa(cfg.NoResponseMinutes))
	setChecked(hAutoStart, cfg.AutoStart)
	setChecked(hWatchdog, cfg.WatchdogEnabled)
	setChecked(hWatchdogAutoStart, cfg.WatchdogAutoStart)
}

func readConfigFromControls() Config {
	return Config{
		ProgramDir:        strings.TrimSpace(getText(hProgramDir)),
		ServerChanSendKey: strings.TrimSpace(getText(hSendKey)),
		PushDelayMinutes:  atoiDefault(getText(hDelay), defaultDelay),
		NoResponseMinutes: atoiDefault(getText(hNoResp), defaultStale),
		AutoStart:         isChecked(hAutoStart),
		WatchdogEnabled:   isChecked(hWatchdog),
		WatchdogAutoStart: isChecked(hWatchdogAutoStart),
	}
}

func saveConfigFromControls() {
	cfg := readConfigFromControls()
	if strings.TrimSpace(cfg.ProgramDir) == "" {
		messageBox("请选择目录", "请先选择或填写打印程序目录。\r\n日志将按：打印程序目录\\Log\\main\\Log[yyyy_mm_dd].txt 自动读取。")
		return
	}
	cfg = normalizeConfig(cfg)
	if err := saveConfig(cfg); err != nil {
		messageBox("保存失败", err.Error())
		return
	}
	if err := applyAutoStartSetting(cfg.AutoStart); err != nil {
		messageBox("自启动设置失败", err.Error())
		debugLogf("apply autostart failed enabled=%v err=%v", cfg.AutoStart, err)
		return
	}
	state.setConfig(cfg)
	loadConfigToControls()
	messageBox("保存成功", "配置已保存到：\r\n"+configPath)
}

func testPush() {
	cfg := readConfigFromControls()
	cfg = normalizeConfig(cfg)
	alert := &Alert{
		Kind:      "test",
		Title:     "测试推送",
		Detail:    "这是一条来自 UV 打印机日志监控工具的测试推送。",
		CreatedAt: time.Now(),
		DueAt:     time.Now(),
	}
	go func() {
		result := sendServerChan(cfg, alert)
		state.addEvent("测试推送结果：" + result)
	}()
}

func updateUI() {
	state.mu.Lock()
	cfg := state.cfg
	status := state.status
	detail := buildDetailLocked()
	events := strings.Join(state.events, "\r\n")
	logPath := state.logPath
	programDir := cfg.ProgramDir
	state.mu.Unlock()

	if strings.TrimSpace(getText(hProgramDir)) == "" && programDir != "" {
		setText(hProgramDir, programDir)
	}
	if strings.TrimSpace(getText(hLogPath)) == "" && logPath != "" {
		setText(hLogPath, logPath)
	}
	if logPath != "" {
		setText(hLogPath, logPath)
	} else if programDir != "" {
		setText(hLogPath, logPathForDate(programDir, time.Now()))
	}
	if strings.TrimSpace(getText(hDelay)) == "" {
		setText(hDelay, strconv.Itoa(cfg.PushDelayMinutes))
	}
	if strings.TrimSpace(getText(hNoResp)) == "" {
		setText(hNoResp, strconv.Itoa(cfg.NoResponseMinutes))
	}
	setText(hStatus, "状态："+status)
	setText(hDetail, detail)
	setText(hEvents, events)
}

func buildDetailLocked() string {
	lines := []string{}
	if state.currentTask != "" {
		lines = append(lines, "任务："+state.currentTask)
	}
	if state.lastLogStamp != "" {
		lines = append(lines, "最后日志时间："+state.lastLogStamp)
	}
	if state.lastLogLineTime.IsZero() {
		lines = append(lines, "尚未读取到日志")
	} else {
		lines = append(lines, "距最后日志："+formatDuration(time.Since(state.lastLogLineTime)))
	}
	if state.statusKind == "printing" {
		if state.lastPrintProgressTime.IsZero() {
			lines = append(lines, "尚未识别到打印进度")
		} else {
			progressText := "距最后打印进度：" + formatDuration(time.Since(state.lastPrintProgressTime))
			if state.lastPrintProgressStamp != "" {
				progressText += "（" + state.lastPrintProgressStamp + "）"
			}
			lines = append(lines, progressText)
		}
	}
	if state.activeAlert != nil {
		remain := time.Until(state.activeAlert.DueAt)
		if remain < 0 {
			remain = 0
		}
		lines = append(lines, fmt.Sprintf("待确认：%s，剩余 %s 自动推送", state.activeAlert.Title, formatDuration(remain)))
		if state.activeAlert.SendResult != "" {
			lines = append(lines, "推送结果："+state.activeAlert.SendResult)
		}
	}
	if state.logPath != "" {
		lines = append(lines, "监控："+state.logPath)
	}
	return strings.Join(lines, "    ")
}

func formatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d.Seconds() + 0.5)
	if total < 60 {
		return fmt.Sprintf("%d秒", total)
	}
	return fmt.Sprintf("%02d:%02d", total/60, total%60)
}

func atoiDefault(s string, def int) int {
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || v <= 0 {
		return def
	}
	return v
}

func setText(hwnd uintptr, text string) {
	p := utf16Ptr(text)
	procSetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(p)))
}

func getText(hwnd uintptr) string {
	if hwnd == 0 {
		return ""
	}
	l, _, _ := procGetWindowTextLength.Call(hwnd)
	buf := make([]uint16, int(l)+2)
	procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf)
}

func setChecked(hwnd uintptr, checked bool) {
	if hwnd == 0 {
		return
	}
	value := uintptr(0)
	if checked {
		value = BST_CHECKED
	}
	procSendMessageW.Call(hwnd, BM_SETCHECK, value, 0)
}

func isChecked(hwnd uintptr) bool {
	if hwnd == 0 {
		return false
	}
	r, _, _ := procSendMessageW.Call(hwnd, BM_GETCHECK, 0, 0)
	return r == BST_CHECKED
}

func utf16Ptr(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

func messageBox(title, text string) {
	t := utf16Ptr(title)
	m := utf16Ptr(text)
	procMessageBoxW.Call(hMain, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), 0)
}

func chooseFolder(owner uintptr) string {
	display := make([]uint16, 260)
	title := utf16Ptr("请选择打印程序目录")
	bi := BrowseInfo{
		HwndOwner:      owner,
		PszDisplayName: &display[0],
		LpszTitle:      title,
		UlFlags:        0x0001 | 0x0040,
	}
	pidl, _, _ := procSHBrowseForFolderW.Call(uintptr(unsafe.Pointer(&bi)))
	if pidl == 0 {
		return ""
	}
	defer procCoTaskMemFree.Call(pidl)
	pathBuf := make([]uint16, 32768)
	ok, _, _ := procSHGetPathFromIDList.Call(pidl, uintptr(unsafe.Pointer(&pathBuf[0])))
	if ok == 0 {
		return ""
	}
	return syscall.UTF16ToString(pathBuf)
}
