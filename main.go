package main

import (
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

//go:embed Icon.ico
var embeddedIcon []byte

// generateToken creates a URL-safe base64 token.
func generateToken(nBytes int) string {
	b := make([]byte, nBytes)
	_, _ = rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)
}

// openBrowser opens the default browser on the given URL.
func openBrowser(url string) {
	if runtime.GOOS == "windows" {
		_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	} else {
		_ = exec.Command("xdg-open", url).Start()
	}
}

// waitForShutdown blocks until an OS signal or the shutdown channel fires.
func waitForShutdown(shutdownCh chan struct{}) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	select {
	case <-sigCh:
	case <-shutdownCh:
	}
}

// ── System Tray (Windows) ───────────────────────────────────────

const (
	wmUser       = 0x0400
	wmTrayIcon   = wmUser + 1
	wmCommand    = 0x0111
	wmDestroy    = 0x0002
	wmLButtonDbl = 0x0203
	wmRButtonUp  = 0x0205

	nimAdd    = 0x00000000
	nimDelete = 0x00000002
	nifIcon    = 0x00000002
	nifMessage = 0x00000001
	nifTip     = 0x00000004

	tpmLeftAlign   = 0x0000
	tpmBottomAlign = 0x0020

	idiApplication = 32512
	idcArrow       = 32512
	wsOverlapped   = 0x00000000
	csHRedraw      = 0x0002
	csVRedraw      = 0x0001
	mfString       = 0x00000000
	mfSeparator    = 0x00000800
	swHide         = 0
	idOpen         = 1001
	idQuit         = 1002
)

type notifyIconData struct {
	CbSize           uint32
	HWnd             windows.HWND
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            windows.Handle
	SzTip            [128]uint16
}

type wndClassEx struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     windows.Handle
	HIcon         windows.Handle
	HCursor       windows.Handle
	HbrBackground windows.Handle
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       windows.Handle
}

type point struct {
	X, Y int32
}

type msg struct {
	HWnd    windows.HWND
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	kernel32win = windows.NewLazySystemDLL("kernel32.dll")

	pShellNotifyIconW = shell32.NewProc("Shell_NotifyIconW")
	pRegisterClassExW = user32.NewProc("RegisterClassExW")
	pCreateWindowExW  = user32.NewProc("CreateWindowExW")
	pDefWindowProcW   = user32.NewProc("DefWindowProcW")
	pGetMessageW      = user32.NewProc("GetMessageW")
	pTranslateMessage = user32.NewProc("TranslateMessage")
	pDispatchMessageW = user32.NewProc("DispatchMessageW")
	pPostQuitMessage  = user32.NewProc("PostQuitMessage")
	pPostMessageW     = user32.NewProc("PostMessageW")
	pDestroyWindow    = user32.NewProc("DestroyWindow")
	pShowWindow       = user32.NewProc("ShowWindow")
	pCreatePopupMenu  = user32.NewProc("CreatePopupMenu")
	pAppendMenuW      = user32.NewProc("AppendMenuW")
	pTrackPopupMenu   = user32.NewProc("TrackPopupMenu")
	pDestroyMenu      = user32.NewProc("DestroyMenu")
	pSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	pGetCursorPos     = user32.NewProc("GetCursorPos")
	pLoadIconW              = user32.NewProc("LoadIconW")
	pCreateIconFromResourceEx = user32.NewProc("CreateIconFromResourceEx")
	pLoadCursorW      = user32.NewProc("LoadCursorW")
	pGetModuleHandleW = kernel32win.NewProc("GetModuleHandleW")
)

var (
	trayHWnd   windows.HWND
	trayURL    string
	trayOnQuit func()
	// prevent GC of callback
	trayWndProcCb uintptr
)

func trayWndProc(hwnd windows.HWND, uMsg uint32, wParam, lParam uintptr) uintptr {
	switch uMsg {
	case wmTrayIcon:
		if lParam == wmRButtonUp {
			showContextMenu(hwnd)
			return 0
		}
		if lParam == wmLButtonDbl {
			openBrowser(trayURL)
			return 0
		}
	case wmCommand:
		cmdID := wParam & 0xFFFF
		if cmdID == idOpen {
			openBrowser(trayURL)
			return 0
		}
		if cmdID == idQuit {
			removeTrayIcon(hwnd)
			pDestroyWindow.Call(uintptr(hwnd))
			pPostQuitMessage.Call(0)
			trayHWnd = 0
			if trayOnQuit != nil {
				go trayOnQuit()
			}
			return 0
		}
	case wmDestroy:
		removeTrayIcon(hwnd)
		pPostQuitMessage.Call(0)
		trayHWnd = 0
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(uintptr(hwnd), uintptr(uMsg), wParam, lParam)
	return r
}

func showContextMenu(hwnd windows.HWND) {
	hMenu, _, _ := pCreatePopupMenu.Call()
	openText, _ := windows.UTF16PtrFromString("Открыть в браузере")
	quitText, _ := windows.UTF16PtrFromString("Выход")
	pAppendMenuW.Call(hMenu, mfString, idOpen, uintptr(unsafe.Pointer(openText)))
	pAppendMenuW.Call(hMenu, mfSeparator, 0, 0)
	pAppendMenuW.Call(hMenu, mfString, idQuit, uintptr(unsafe.Pointer(quitText)))

	var pt point
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	pSetForegroundWindow.Call(uintptr(hwnd))
	pTrackPopupMenu.Call(hMenu, tpmLeftAlign|tpmBottomAlign, uintptr(pt.X), uintptr(pt.Y), 0, uintptr(hwnd), 0)
	pDestroyMenu.Call(hMenu)
}

func removeTrayIcon(hwnd windows.HWND) {
	var nid notifyIconData
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.HWnd = hwnd
	nid.UID = 1
	pShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
}

func startTrayIcon(url string, onQuit func()) {
	trayURL = url
	trayOnQuit = onQuit
	go trayMain()
}

func loadEmbeddedIcon() uintptr {
	if len(embeddedIcon) < 22 {
		return 0
	}
	// .ico file format: 6-byte header + 16-byte entries
	// Header: reserved(2) + type(2) + count(2)
	// Entry: width(1) + height(1) + colors(1) + reserved(1) + planes(2) + bitcount(2) + size(4) + offset(4)
	// We need to find the best 32x32 icon entry and pass its raw data to CreateIconFromResourceEx

	count := int(embeddedIcon[4]) | int(embeddedIcon[5])<<8
	if count == 0 {
		return 0
	}

	// Find best entry (prefer 32x32, fallback to first)
	bestIdx := 0
	for i := 0; i < count; i++ {
		off := 6 + i*16
		if off+16 > len(embeddedIcon) {
			break
		}
		w := int(embeddedIcon[off])
		h := int(embeddedIcon[off+1])
		if w == 32 && h == 32 {
			bestIdx = i
			break
		}
	}

	entryOff := 6 + bestIdx*16
	if entryOff+16 > len(embeddedIcon) {
		return 0
	}

	dataSize := int(embeddedIcon[entryOff+8]) | int(embeddedIcon[entryOff+9])<<8 |
		int(embeddedIcon[entryOff+10])<<16 | int(embeddedIcon[entryOff+11])<<24
	dataOffset := int(embeddedIcon[entryOff+12]) | int(embeddedIcon[entryOff+13])<<8 |
		int(embeddedIcon[entryOff+14])<<16 | int(embeddedIcon[entryOff+15])<<24

	if dataOffset+dataSize > len(embeddedIcon) {
		return 0
	}

	iconData := embeddedIcon[dataOffset : dataOffset+dataSize]

	h, _, _ := pCreateIconFromResourceEx.Call(
		uintptr(unsafe.Pointer(&iconData[0])),
		uintptr(dataSize),
		1,       // fIcon = TRUE
		0x30000, // version
		32, 32,  // cx, cy
		0, // flags
	)
	return h
}

func trayMain() {
	runtime.LockOSThread()

	hInstance, _, _ := pGetModuleHandleW.Call(0)
	hIcon := loadEmbeddedIcon()
	if hIcon == 0 {
		hIcon, _, _ = pLoadIconW.Call(0, idiApplication)
	}
	hCursor, _, _ := pLoadCursorW.Call(0, idcArrow)

	className, _ := windows.UTF16PtrFromString("OnecGatewayTray")
	windowTitle, _ := windows.UTF16PtrFromString("1C Trusted Gateway")

	trayWndProcCb = windows.NewCallback(trayWndProc)

	wc := wndClassEx{
		CbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
		Style:         csHRedraw | csVRedraw,
		LpfnWndProc:   trayWndProcCb,
		HInstance:     windows.Handle(hInstance),
		HIcon:         windows.Handle(hIcon),
		HCursor:       windows.Handle(hCursor),
		LpszClassName: className,
	}
	atom, _, _ := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	if atom == 0 {
		return
	}

	hwnd, _, _ := pCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(windowTitle)),
		wsOverlapped,
		0, 0, 0, 0,
		0, 0, hInstance, 0,
	)
	if hwnd == 0 {
		return
	}
	trayHWnd = windows.HWND(hwnd)
	pShowWindow.Call(hwnd, swHide)

	// Add tray icon
	var nid notifyIconData
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.HWnd = trayHWnd
	nid.UID = 1
	nid.UFlags = nifIcon | nifMessage | nifTip
	nid.UCallbackMessage = wmTrayIcon
	nid.HIcon = windows.Handle(hIcon)
	tip, _ := windows.UTF16FromString("1C Trusted Gateway")
	copy(nid.SzTip[:], tip)

	pShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))

	// Message loop
	var m msg
	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if r == 0 || int32(r) == -1 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func stopTrayIcon() {
	if trayHWnd != 0 {
		pPostMessageW.Call(uintptr(trayHWnd), wmDestroy, 0, 0)
	}
}

// app is the global TrustedWebApp instance, referenced by the SSE handler.
var app *TrustedWebApp

func main() {
	configPath := "config.json"
	if len(os.Args) > 1 {
		configPath = os.Args[1]
	}

	// Suppress console window in Windows GUI mode
	fmt.Println("1C Trusted Gateway starting...")

	// Use LaunchWeb which handles everything
	savedToken := ""
	var config *AppConfig

	if HasSavedSettings() {
		settingsData, _ := LoadSettings()
		if settingsData != nil {
			config = ConfigFromDict(settingsData)
			if authMap, ok := settingsData["auth"].(map[string]any); ok {
				if tok, ok := authMap["token"].(string); ok {
					savedToken = tok
				}
			}
		}
	}

	if config == nil {
		var err error
		config, err = LoadConfig(configPath)
		if err != nil {
			config = DefaultAppConfig()
		}
	}

	app = NewTrustedWebApp(config, savedToken)

	// Start TCP bridge
	if err := app.Bridge.Start(); err != nil {
		fmt.Printf("Failed to start TCP bridge: %v\n", err)
	} else {
		fmt.Printf("TCP bridge: %s:%d\n", DefaultBridgeHost, DefaultBridgePort)
	}

	// Start HTTP server
	host := DefaultWebHost
	port := config.WebPort
	if port <= 0 {
		port = DefaultWebPort
	}

	httpd := NewWebHTTPServer(host, port, app)

	webURL := fmt.Sprintf("http://%s:%d/?token=%s", host, port, app.SessionToken)
	fmt.Printf("Web UI: %s\n", webURL)

	// Open browser
	openBrowser(webURL)

	// Start system tray
	shutdownCh := make(chan struct{}, 1)
	startTrayIcon(webURL, func() {
		select {
		case shutdownCh <- struct{}{}:
		default:
		}
	})

	// Start HTTP server in goroutine
	go func() {
		if err := httpd.ListenAndServe(); err != nil {
			fmt.Printf("HTTP server error: %v\n", err)
		}
	}()

	// Wait for shutdown signal
	waitForShutdown(shutdownCh)

	fmt.Println("\nЗавершение...")
	stopTrayIcon()
	app.Shutdown()
	httpd.ShutdownServer()
}
