package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	appDirName  = "OnecGateway"
	fileNameWin = "settings.bin"
)

// Same entropy bytes as Python: b"1C-TrustedGateway-v1-\xc0\x9f\xd0\xb5\xd1\x80\xd0\xb5"
var dpapiEntropy = []byte{
	'1', 'C', '-', 'T', 'r', 'u', 's', 't', 'e', 'd',
	'G', 'a', 't', 'e', 'w', 'a', 'y', '-', 'v', '1', '-',
	0xc0, 0x9f, 0xd0, 0xb5, 0xd1, 0x80, 0xd0, 0xb5,
}

func settingsPath() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, "AppData", "Local")
	}
	return filepath.Join(base, appDirName, fileNameWin)
}

// dataBlob mirrors the Windows DATA_BLOB structure.
type dataBlob struct {
	cbData uint32
	pbData *byte
}

func newDataBlob(data []byte) dataBlob {
	if len(data) == 0 {
		return dataBlob{}
	}
	return dataBlob{
		cbData: uint32(len(data)),
		pbData: &data[0],
	}
}

func (b *dataBlob) bytes() []byte {
	if b.cbData == 0 || b.pbData == nil {
		return nil
	}
	return unsafe.Slice(b.pbData, b.cbData)
}

var (
	modCrypt32  = windows.NewLazySystemDLL("crypt32.dll")
	modKernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procCryptProtectData   = modCrypt32.NewProc("CryptProtectData")
	procCryptUnprotectData = modCrypt32.NewProc("CryptUnprotectData")
	procLocalFree          = modKernel32.NewProc("LocalFree")
)

func dpapiEncrypt(plaintext []byte) ([]byte, error) {
	blobIn := newDataBlob(plaintext)
	entropyBlob := newDataBlob(dpapiEntropy)
	var blobOut dataBlob

	r, _, err := procCryptProtectData.Call(
		uintptr(unsafe.Pointer(&blobIn)),
		0, // szDataDescr
		uintptr(unsafe.Pointer(&entropyBlob)),
		0, // pvReserved
		0, // pPromptStruct
		0, // dwFlags
		uintptr(unsafe.Pointer(&blobOut)),
	)
	if r == 0 {
		return nil, err
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(blobOut.pbData)))

	result := make([]byte, blobOut.cbData)
	copy(result, blobOut.bytes())
	return result, nil
}

func dpapiDecrypt(ciphertext []byte) ([]byte, error) {
	blobIn := newDataBlob(ciphertext)
	entropyBlob := newDataBlob(dpapiEntropy)
	var blobOut dataBlob

	r, _, err := procCryptUnprotectData.Call(
		uintptr(unsafe.Pointer(&blobIn)),
		0, // ppszDataDescr
		uintptr(unsafe.Pointer(&entropyBlob)),
		0, // pvReserved
		0, // pPromptStruct
		0, // dwFlags
		uintptr(unsafe.Pointer(&blobOut)),
	)
	if r == 0 {
		return nil, err
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(blobOut.pbData)))

	result := make([]byte, blobOut.cbData)
	copy(result, blobOut.bytes())
	return result, nil
}

// SaveSettings encrypts data via DPAPI and writes to disk.
func SaveSettings(data map[string]any) error {
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	path := settingsPath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	encrypted, err := dpapiEncrypt(raw)
	if err != nil {
		return err
	}
	return os.WriteFile(path, encrypted, 0o600)
}

// LoadSettings decrypts and returns saved settings. Returns nil, nil if no file exists.
func LoadSettings() (map[string]any, error) {
	path := settingsPath()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil
	}
	plaintext, err := dpapiDecrypt(data)
	if err != nil {
		return nil, nil
	}
	var result map[string]any
	if err := json.Unmarshal(plaintext, &result); err != nil {
		return nil, nil
	}
	return result, nil
}

// DeleteSettings removes the persisted settings file.
func DeleteSettings() error {
	path := settingsPath()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	return os.Remove(path)
}

// HasSavedSettings checks whether a settings file exists on disk.
func HasSavedSettings() bool {
	path := settingsPath()
	_, err := os.Stat(path)
	return err == nil
}
