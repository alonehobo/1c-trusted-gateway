package main

import (
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ── System Tray (Windows) ───────────────────────────────────────

const (
	wmUser       = 0x0400
	wmTrayIcon   = wmUser + 1
	wmCommand    = 0x0111
	wmDestroy    = 0x0002
	wmLButtonDbl = 0x0203
	wmRButtonUp  = 0x0205

	nimAdd        = 0x00000000
	nimDelete     = 0x00000002
	nimSetVersion = 0x00000004
	nifIcon    = 0x00000002
	nifMessage = 0x00000001
	nifTip     = 0x00000004
	nifInfo    = 0x00000010

	niifInfo    = 0x00000001
	nimModify   = 0x00000001

	ninBalloonUserClick = 0x0405 // user clicked balloon

	flashwTimerCount = 0x0004 // FLASHW_TIMERNOFG — flash until foreground
	flashwAll        = 0x0003 // FLASHW_ALL — flash caption + taskbar

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
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
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
	user32      = windows.NewLazySystemDLL("user32.dll")
	shell32     = windows.NewLazySystemDLL("shell32.dll")
	kernel32win = windows.NewLazySystemDLL("kernel32.dll")

	pShellNotifyIconW         = shell32.NewProc("Shell_NotifyIconW")
	pRegisterClassExW         = user32.NewProc("RegisterClassExW")
	pCreateWindowExW          = user32.NewProc("CreateWindowExW")
	pDefWindowProcW           = user32.NewProc("DefWindowProcW")
	pGetMessageW              = user32.NewProc("GetMessageW")
	pTranslateMessage         = user32.NewProc("TranslateMessage")
	pDispatchMessageW         = user32.NewProc("DispatchMessageW")
	pPostQuitMessage          = user32.NewProc("PostQuitMessage")
	pPostMessageW             = user32.NewProc("PostMessageW")
	pDestroyWindow            = user32.NewProc("DestroyWindow")
	pShowWindow               = user32.NewProc("ShowWindow")
	pCreatePopupMenu          = user32.NewProc("CreatePopupMenu")
	pAppendMenuW              = user32.NewProc("AppendMenuW")
	pTrackPopupMenu           = user32.NewProc("TrackPopupMenu")
	pDestroyMenu              = user32.NewProc("DestroyMenu")
	pSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	pGetCursorPos             = user32.NewProc("GetCursorPos")
	pLoadIconW                = user32.NewProc("LoadIconW")
	pCreateIconFromResourceEx = user32.NewProc("CreateIconFromResourceEx")
	pLoadCursorW              = user32.NewProc("LoadCursorW")
	pGetModuleHandleW         = kernel32win.NewProc("GetModuleHandleW")
	pFlashWindowEx            = user32.NewProc("FlashWindowEx")
	pFindWindowW              = user32.NewProc("FindWindowW")
	pEnumWindows              = user32.NewProc("EnumWindows")
	pGetWindowTextW           = user32.NewProc("GetWindowTextW")
	pGetWindowTextLenW        = user32.NewProc("GetWindowTextLengthW")
	pIsWindowVisible          = user32.NewProc("IsWindowVisible")
	pShowWindowAsync          = user32.NewProc("ShowWindowAsync")
)

type flashWInfo struct {
	CbSize    uint32
	Hwnd      windows.HWND
	DwFlags   uint32
	UCount    uint32
	DwTimeout uint32
}

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
		if lParam == ninBalloonUserClick {
			focusBrowserWindow()
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

// findBrowserWindow finds a visible window whose title contains substr.
func findBrowserWindow(substr string) windows.HWND {
	var found windows.HWND
	subLower := strings.ToLower(substr)
	cb := windows.NewCallback(func(hwnd windows.HWND, lParam uintptr) uintptr {
		vis, _, _ := pIsWindowVisible.Call(uintptr(hwnd))
		if vis == 0 {
			return 1 // continue
		}
		tLen, _, _ := pGetWindowTextLenW.Call(uintptr(hwnd))
		if tLen == 0 {
			return 1
		}
		buf := make([]uint16, tLen+1)
		pGetWindowTextW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&buf[0])), tLen+1)
		title := strings.ToLower(windows.UTF16ToString(buf))
		if strings.Contains(title, subLower) {
			found = hwnd
			return 0 // stop
		}
		return 1
	})
	pEnumWindows.Call(cb, 0)
	return found
}

// findGatewayBrowserWindow tries multiple title patterns to find the browser window.
// Title may alternate due to blinking ("⚡ Требуется действие" vs "Trusted Gateway").
func findGatewayBrowserWindow() windows.HWND {
	for _, pattern := range []string{"trusted gateway", "требуется действие"} {
		if hwnd := findBrowserWindow(pattern); hwnd != 0 {
			return hwnd
		}
	}
	return 0
}

// flashBrowserWindow finds the browser window with Trusted Gateway and flashes it.
func flashBrowserWindow() {
	hwnd := findGatewayBrowserWindow()
	if hwnd == 0 {
		return
	}
	var fi flashWInfo
	fi.CbSize = uint32(unsafe.Sizeof(fi))
	fi.Hwnd = hwnd
	fi.DwFlags = flashwAll | flashwTimerCount
	fi.UCount = 5
	fi.DwTimeout = 0
	pFlashWindowEx.Call(uintptr(unsafe.Pointer(&fi)))
}

// focusBrowserWindow finds the browser window and brings it to foreground.
func focusBrowserWindow() {
	hwnd := findGatewayBrowserWindow()
	if hwnd == 0 {
		openBrowser(trayURL) // fallback
		return
	}
	const swRestore = 9
	pShowWindowAsync.Call(uintptr(hwnd), swRestore)
	pSetForegroundWindow.Call(uintptr(hwnd))
}

// showTrayBalloon shows a native Windows balloon notification from the tray icon.
func showTrayBalloon(title, message string) {
	if trayHWnd == 0 {
		return
	}
	var nid notifyIconData
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.HWnd = trayHWnd
	nid.UID = 1
	nid.UFlags = nifInfo

	// Copy title
	titleUTF16, _ := windows.UTF16FromString(title)
	for i := 0; i < len(titleUTF16) && i < len(nid.SzInfoTitle); i++ {
		nid.SzInfoTitle[i] = titleUTF16[i]
	}
	// Copy message
	msgUTF16, _ := windows.UTF16FromString(message)
	for i := 0; i < len(msgUTF16) && i < len(nid.SzInfo); i++ {
		nid.SzInfo[i] = msgUTF16[i]
	}
	nid.DwInfoFlags = niifInfo

	pShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nid)))

	// Also flash browser taskbar button
	flashBrowserWindow()
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
	count := int(embeddedIcon[4]) | int(embeddedIcon[5])<<8
	if count == 0 {
		return 0
	}

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

	nid.UVersion = 3 // NOTIFYICON_VERSION
	pShellNotifyIconW.Call(nimSetVersion, uintptr(unsafe.Pointer(&nid)))

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
