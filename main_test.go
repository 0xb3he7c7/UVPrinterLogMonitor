package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

func TestLogPathForProgramDir(t *testing.T) {
	root := filepath.Join("C:", "PrinterApp")
	got := logPathForDate(root, time.Date(2026, 6, 26, 0, 0, 0, 0, time.Local))
	want := filepath.Join(root, "Log", "main", "Log[2026_06_26].txt")
	if got != want {
		t.Fatalf("log path = %q, want %q", got, want)
	}
	if inferred := inferProgramDir(want); !strings.EqualFold(inferred, root) {
		t.Fatalf("program dir = %q, want %q", inferred, root)
	}
}

func TestCriticalLineDetection(t *testing.T) {
	lines := []string{
		"[13:40:17.956][软件][错误][012008] 系统异常，防撞触发",
		"[13:40:18.797][软件][调试][000000] 相机SDK--运动X到扫描起始位置 失败!",
	}
	for _, line := range lines {
		if !isCriticalLine(line, strings.ToLower(line)) {
			t.Fatalf("line should be critical: %s", line)
		}
	}
}

func TestCompletionCreatesPendingAlert(t *testing.T) {
	state = &MonitorState{
		cfg:          Config{PushDelayMinutes: 3, NoResponseMinutes: 10},
		status:       "打印中",
		statusKind:   "printing",
		currentTask:  "temp.prn",
		events:       make([]string, 0, 20),
		lastLogStamp: "08:32:21.900",
	}
	handleLogLine("[08:32:21.900][软件][调试][000000] _PrintWait---打印完成", false)
	if state.activeAlert == nil {
		t.Fatal("expected pending alert")
	}
	if state.activeAlert.Kind != "complete" {
		t.Fatalf("alert kind = %q", state.activeAlert.Kind)
	}
	if time.Until(state.activeAlert.DueAt) > time.Second {
		t.Fatalf("completion alert due too late: %s", state.activeAlert.DueAt)
	}
}

func TestEmergencyAlertImmediateAndRepeats(t *testing.T) {
	state = &MonitorState{
		cfg:         Config{PushDelayMinutes: 3, NoResponseMinutes: 10},
		status:      "打印中",
		statusKind:  "printing",
		currentTask: "temp.prn",
		events:      make([]string, 0, 20),
	}
	handleLogLine("[08:32:21.900][软件][错误][012008] 系统异常，防撞触发", false)
	if state.activeAlert == nil {
		t.Fatal("expected emergency alert")
	}
	if state.activeAlert.Kind != "emergency" {
		t.Fatalf("alert kind = %q", state.activeAlert.Kind)
	}
	if time.Until(state.activeAlert.DueAt) > time.Second {
		t.Fatalf("emergency alert due too late: %s", state.activeAlert.DueAt)
	}
	if state.activeAlert.RepeatEvery != 2*time.Minute {
		t.Fatalf("repeat every = %s, want 2m", state.activeAlert.RepeatEvery)
	}
}

func TestFindPrintExpExeSkipsLogDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "Log", "main"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Log", "PrintExp.exe"), []byte("wrong"), 0600); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "bin", "PrintExp.exe")
	if err := os.WriteFile(want, []byte("right"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := findPrintExpExe(dir); got != want {
		t.Fatalf("PrintExp path = %q, want %q", got, want)
	}
}

func TestMidPrintPassInfoMarksPrinting(t *testing.T) {
	state = &MonitorState{
		cfg:    Config{PushDelayMinutes: 3, NoResponseMinutes: 10},
		status: "等待新日志",
		events: make([]string, 0, 20),
	}
	handleLogLine("[08:25:41.489][软件][调试][000000] nCurPass=8: ticktime=1390421", false)
	if state.status != "打印中" {
		t.Fatalf("status = %q, want 打印中", state.status)
	}
}

func TestOrdinaryLogDoesNotRefreshPrintProgress(t *testing.T) {
	progressAt := time.Now().Add(-5 * time.Minute)
	state = &MonitorState{
		cfg:                   Config{PushDelayMinutes: 3, NoResponseMinutes: 10},
		status:                "打印中",
		statusKind:            "printing",
		currentTask:           "temp.prn",
		currentTaskStart:      time.Now().Add(-10 * time.Minute),
		lastPrintProgressTime: progressAt,
		events:                make([]string, 0, 20),
	}
	handleLogLine("[08:26:00.000][software][debug][000000] ordinary heartbeat", false)
	if !state.lastPrintProgressTime.Equal(progressAt) {
		t.Fatalf("ordinary log refreshed print progress: %s", state.lastPrintProgressTime)
	}
}

func TestPassInfoRefreshesPrintProgress(t *testing.T) {
	state = &MonitorState{
		cfg:    Config{PushDelayMinutes: 3, NoResponseMinutes: 10},
		status: "打印中",
		events: make([]string, 0, 20),
	}
	handleLogLine("[08:25:41.489][software][debug][000000] nCurPass=8: ticktime=1390421", false)
	if state.lastPrintProgressTime.IsZero() {
		t.Fatal("expected print progress time")
	}
	if state.lastPrintProgressStamp != "08:25:41.489" {
		t.Fatalf("progress stamp = %q", state.lastPrintProgressStamp)
	}
}

func TestVisualPrintRipLineMarksPrinting(t *testing.T) {
	state = &MonitorState{
		cfg:    Config{PushDelayMinutes: 3, NoResponseMinutes: 10},
		status: "等待打印开始",
		events: make([]string, 0, 20),
	}
	handleLogLine("[19:15:39][software][debug][000000] sRip.nIndex=11,sRip.nSectionWidth=127952,sRip.nPassId:12,nOffsetY:1751", false)
	if state.status != "打印中" {
		t.Fatalf("status = %q, want 打印中", state.status)
	}
	if state.lastPrintProgressTime.IsZero() {
		t.Fatal("expected visual print line to refresh progress")
	}
}

func TestNoResponseUsesPrintProgressNotLastLogGrowth(t *testing.T) {
	now := time.Now()
	state = &MonitorState{
		cfg:                    Config{PushDelayMinutes: 3, NoResponseMinutes: 10},
		status:                 "打印中",
		statusKind:             "printing",
		currentTask:            "temp.prn",
		currentTaskStart:       now.Add(-30 * time.Minute),
		lastLogLineTime:        now,
		lastLogStamp:           "08:40:00.000",
		lastLine:               "[08:40:00.000][software][debug][000000] ordinary heartbeat",
		lastPrintProgressTime:  now.Add(-11 * time.Minute),
		lastPrintProgressStamp: "08:29:00.000",
		lastPrintProgressLine:  "[08:29:00.000][software][debug][000000] nCurPass=8: ticktime=1390421",
		events:                 make([]string, 0, 20),
	}
	state.checkTimers()
	if state.activeAlert == nil {
		t.Fatal("expected no-response alert even though log file is still growing")
	}
	if state.activeAlert.Kind != "noresponse" {
		t.Fatalf("alert kind = %q", state.activeAlert.Kind)
	}
}

func TestHistoricalVisualPrintLineKeepsMidPrintState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Log[2026_06_27].txt")
	content := strings.Join([]string{
		"[19:15:39][software][debug][000000] sRip.nIndex=11,sRip.nSectionWidth=127952,sRip.nPassId:12,nOffsetY:1751",
		"[19:15:39][software][debug][000000] ShowSegContour nPassId:12,nOffsetY:1751",
		"[19:15:39][software][debug][000000] 模板[0504C] 成功识别 252 个产品",
		"[19:15:48][software][debug][000000] 动态贴图耗时:0ms,0",
	}, "\r\n")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	state = &MonitorState{
		cfg:    Config{PushDelayMinutes: 3, NoResponseMinutes: 10},
		status: "读取历史状态",
		events: make([]string, 0, 20),
	}
	parseExistingLog(path)
	if state.status != "打印中" {
		t.Fatalf("status = %q, want 打印中", state.status)
	}
	if state.lastPrintProgressStamp != "19:15:48" {
		t.Fatalf("progress stamp = %q", state.lastPrintProgressStamp)
	}
}

func TestHistoricalTimelineKeepsMidPrintState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Log[2026_06_26].txt")
	content := strings.Join([]string{
		"[08:24:48.036][software][debug][000000] StartPrint Begin",
		"[08:24:51.806][software][debug][000000] waiting for print status",
		"[08:25:41.489][software][debug][000000] nCurPass=8: ticktime=1390421",
		"[08:25:42.418][software][debug][000000] some ordinary tail line",
	}, "\r\n")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	state = &MonitorState{
		cfg:    Config{PushDelayMinutes: 3, NoResponseMinutes: 10},
		status: "读取历史状态",
		events: make([]string, 0, 20),
	}
	parseExistingLog(path)
	if state.status != "打印中" {
		t.Fatalf("status = %q, want 打印中", state.status)
	}
}

func TestHistoricalTimelineWithoutSignalsWaitsForPrintStart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Log[2026_06_27].txt")
	content := strings.Join([]string{
		"[19:09:47.711][software][debug][000000] ordinary startup line",
		"[19:09:48.103][software][debug][000000] ordinary idle line",
	}, "\r\n")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	state = &MonitorState{
		cfg:    Config{PushDelayMinutes: 3, NoResponseMinutes: 10},
		status: "读取历史状态",
		events: make([]string, 0, 20),
	}
	parseExistingLog(path)
	if state.status != "等待打印开始" {
		t.Fatalf("status = %q, want 等待打印开始", state.status)
	}
}

func TestDecodeUTF16LELogLine(t *testing.T) {
	line := "[19:20:19][software][debug][000000] sRip.nIndex=11,sRip.nSectionWidth=127952\r\n"
	got := decodeBytes(utf16LEBytes(line))
	if got != line {
		t.Fatalf("decoded = %q, want %q", got, line)
	}
}

func TestConsumeUTF16LETailMarksPrinting(t *testing.T) {
	state = &MonitorState{
		cfg:    Config{PushDelayMinutes: 3, NoResponseMinutes: 10},
		status: "等待打印开始",
		events: make([]string, 0, 20),
	}
	line := "[19:20:19][software][debug][000000] sRip.nIndex=11,sRip.nSectionWidth=127952\r\n"
	remaining := consumeTailBytes(utf16LEBytes(line), false)
	if len(remaining) != 0 {
		t.Fatalf("remaining bytes = %d, want 0", len(remaining))
	}
	if state.status != "打印中" {
		t.Fatalf("status = %q, want 打印中", state.status)
	}
	if state.lastPrintProgressLine == "[" {
		t.Fatal("progress line was truncated to '['")
	}
}

func utf16LEBytes(s string) []byte {
	encoded := utf16.Encode([]rune(s))
	out := make([]byte, 0, len(encoded)*2)
	for _, v := range encoded {
		out = append(out, byte(v), byte(v>>8))
	}
	return out
}
