// Debug helper: decrypts settings.bin via DPAPI and prints JSON.
// Run with: go run ./cmd/dump-settings
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

type dataBlob struct {
	cbData uint32
	pbData *byte
}

func (b *dataBlob) bytes() []byte {
	if b.pbData == nil || b.cbData == 0 {
		return nil
	}
	return unsafe.Slice(b.pbData, b.cbData)
}

var (
	crypt32           = syscall.NewLazyDLL("crypt32.dll")
	kernel32          = syscall.NewLazyDLL("kernel32.dll")
	procCryptUnprotect = crypt32.NewProc("CryptUnprotectData")
	procLocalFree      = kernel32.NewProc("LocalFree")
)

var entropy = []byte{
	'1', 'C', '-', 'T', 'r', 'u', 's', 't', 'e', 'd',
	'G', 'a', 't', 'e', 'w', 'a', 'y', '-', 'v', '1', '-',
	0xc0, 0x9f, 0xd0, 0xb5, 0xd1, 0x80, 0xd0, 0xb5,
}

func dpapiDecrypt(ciphertext []byte) ([]byte, error) {
	in := dataBlob{cbData: uint32(len(ciphertext)), pbData: &ciphertext[0]}
	ent := dataBlob{cbData: uint32(len(entropy)), pbData: &entropy[0]}
	var out dataBlob
	r, _, err := procCryptUnprotect.Call(
		uintptr(unsafe.Pointer(&in)),
		0,
		uintptr(unsafe.Pointer(&ent)),
		0, 0, 0,
		uintptr(unsafe.Pointer(&out)),
	)
	if r == 0 {
		return nil, err
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	result := make([]byte, out.cbData)
	copy(result, out.bytes())
	return result, nil
}

func main() {
	base := os.Getenv("LOCALAPPDATA")
	path := filepath.Join(base, "OnecGateway", "settings.bin")
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Println("read error:", err)
		return
	}
	fmt.Printf("Encrypted size: %d bytes\n", len(data))
	plain, err := dpapiDecrypt(data)
	if err != nil {
		fmt.Println("decrypt error:", err)
		return
	}

	var parsed map[string]any
	if err := json.Unmarshal(plain, &parsed); err != nil {
		fmt.Println("unmarshal error:", err)
		fmt.Println("raw plaintext:", string(plain))
		return
	}

	// Print keys summary
	fmt.Println("Keys in settings.bin:")
	for k := range parsed {
		switch v := parsed[k].(type) {
		case string:
			if len(v) > 100 {
				fmt.Printf("  %s: (string, %d chars): %s...\n", k, len(v), v[:80])
			} else {
				fmt.Printf("  %s: (string): %q\n", k, v)
			}
		case map[string]any:
			fmt.Printf("  %s: (object, %d keys)\n", k, len(v))
		default:
			fmt.Printf("  %s: %v\n", k, v)
		}
	}

	fmt.Println("\nFull JSON:")
	pretty, _ := json.MarshalIndent(parsed, "", "  ")
	fmt.Println(string(pretty))
}
