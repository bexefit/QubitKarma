package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"

	"github.com/cloudflare/circl/kem"
	"github.com/cloudflare/circl/kem/mlkem/mlkem768"
	"github.com/cloudflare/circl/sign"
	"github.com/cloudflare/circl/sign/mldsa/mldsa65"
	"github.com/manifoldco/promptui"
	"github.com/tyler-smith/go-bip39"
	"golang.org/x/crypto/hkdf"
	"golang.org/x/term"
)

// BundleRecord represents a saved encrypted cipher bundle
type BundleRecord struct {
	Name   string `json:"name"`
	Bundle string `json:"bundle"`
}

type BundleFile struct {
	Format  string         `json:"format"`
	Version int            `json:"version"`
	Bundles []BundleRecord `json:"bundles"`
}

type VaultState struct {
	KemPublicKey kem.PublicKey
	KemSecretKey kem.PrivateKey
	DsaPublicKey sign.PublicKey
	DsaSecretKey sign.PrivateKey
}

// getBundleFilePath returns the path to bundle.json in the current working directory
func getBundleFilePath() string {
	return "bundle.json"
}

func main() {
	fmt.Println("=== PQC Vault CLI (Internal Key Management) ===")

	for {
		prompt := promptui.Select{
			Label: "Get Started",
			Items: []string{
				"Unlock with Seed Phrase",
				"Create New Seed Phrase",
				"Exit",
			},
		}

		_, result, err := prompt.Run()
		if err != nil {
			fmt.Println("Exiting...")
			break
		}

		switch result {
		case "Create New Seed Phrase":
			mnemonic, err := createNewSeed()
			if err != nil {
				fmt.Printf("[Error] Failed to generate seed: %v\n", err)
				continue
			}
			state, err := deriveKeysFromMnemonic(mnemonic)
			if err != nil {
				fmt.Printf("[Error] Key derivation failed: %v\n", err)
				continue
			}
			runVaultDashboard(state)

		case "Unlock with Seed Phrase":
			fmt.Print("Enter your 12 or 24-word seed phrase (input is hidden): ")
			byteSeed, err := term.ReadPassword(int(syscall.Stdin))
			fmt.Println() 
			
			if err != nil {
				fmt.Println("\n[Error] Failed to read seed phrase.")
				continue
			}
			
			mnemonic := strings.TrimSpace(strings.ToLower(string(byteSeed)))
			if !bip39.IsMnemonicValid(mnemonic) {
				fmt.Println("[Error] Invalid seed phrase. Please check your words.")
				continue
			}
			
			state, err := deriveKeysFromMnemonic(mnemonic)
			if err != nil {
				fmt.Printf("[Error] Key derivation failed: %v\n", err)
				continue
			}
			runVaultDashboard(state)

		case "Exit":
			fmt.Println("Goodbye!")
			return
		}
	}
}

func createNewSeed() (string, error) {
	entropy, err := bip39.NewEntropy(256) // 256 bits = 24 words
	if err != nil {
		return "", err
	}
	mnemonic, err := bip39.NewMnemonic(entropy)
	if err != nil {
		return "", err
	}

	fmt.Println("\n==================================================")
	fmt.Println("NEW SEED PHRASE (Write this down securely offline!):")
	fmt.Println(mnemonic)
	fmt.Println("==================================================")
	return mnemonic, nil
}

func deriveKeysFromMnemonic(mnemonic string) (*VaultState, error) {
	masterSeed := bip39.NewSeed(mnemonic, "") // 64 bytes
	salt := []byte("pqc-vault-seed-hkdf-v2-768-65")

	// Derive ML-KEM-768 seed (64 bytes)
	kemReader := hkdf.New(sha512.New, masterSeed, salt, []byte("ML-KEM-768"))
	kemSeed := make([]byte, 64)
	if _, err := io.ReadFull(kemReader, kemSeed); err != nil {
		return nil, err
	}

	// Derive ML-DSA-65 seed (32 bytes)
	dsaReader := hkdf.New(sha512.New, masterSeed, salt, []byte("ML-DSA-65"))
	dsaSeedBytes := make([]byte, 32)
	if _, err := io.ReadFull(dsaReader, dsaSeedBytes); err != nil {
		return nil, err
	}

	// Generate ML-KEM-768 keys via Scheme interface
	kemSch := mlkem768.Scheme()
	kemPk, kemSk := kemSch.DeriveKeyPair(kemSeed)

	// Generate ML-DSA-65 keys via Scheme interface
	dsaSch := mldsa65.Scheme()
	dsaPk, dsaSk := dsaSch.DeriveKey(dsaSeedBytes)

	fmt.Println("[Success] Vault keys derived and loaded securely.")
	return &VaultState{
		KemPublicKey: kemPk,
		KemSecretKey: kemSk,
		DsaPublicKey: dsaPk,
		DsaSecretKey: dsaSk,
	}, nil
}

func runVaultDashboard(state *VaultState) {
	for {
		prompt := promptui.Select{
			Label: "Active Vault Menu",
			Items: []string{
				"Save a Message",
				"Read a Message",
				"Delete a Message",
				"Return",
			},
		}

		_, choice, err := prompt.Run()
		if err != nil {
			break
		}

		switch choice {
		case "Save a Message":
			handleEncrypt(state)
		case "Read a Message":
			handleLoadBundleMenu(state)
		case "Delete a Message":
			handleDeleteBundleMenu(state)
		case "Return":
			fmt.Println("Locking keys and returning...")
			return
		}
	}
}

func handleEncrypt(state *VaultState) {
	msgPrompt := promptui.Prompt{
		Label: "Plaintext Message",
	}
	plainText, err := msgPrompt.Run()
	if err != nil {
		return
	}

	// 1. Sign message internally with own ML-DSA-65 secret key
	msgBytes := []byte(plainText)
	dsaSch := mldsa65.Scheme()
	sig := dsaSch.Sign(state.DsaSecretKey, msgBytes, nil)

	// 2. Format payload: [4-byte big-endian message length] || message || signature
	payload := make([]byte, 4+len(msgBytes)+len(sig))
	payload[0] = byte(len(msgBytes) >> 24)
	payload[1] = byte(len(msgBytes) >> 16)
	payload[2] = byte(len(msgBytes) >> 8)
	payload[3] = byte(len(msgBytes))
	copy(payload[4:], msgBytes)
	copy(payload[4+len(msgBytes):], sig)

	// 3. Encapsulate shared secret using internal ML-KEM-768 public key
	kemSch := mlkem768.Scheme()
	kemCiphertext, sharedSecret, err := kemSch.Encapsulate(state.KemPublicKey)
	if err != nil {
		fmt.Printf("[Error] Encapsulation failed: %v\n", err)
		return
	}

	// 4. Encrypt payload using AES-GCM with shared secret
	block, err := aes.NewCipher(sharedSecret)
	if err != nil {
		fmt.Printf("[Error] AES cipher init failed: %v\n", err)
		return
	}

	aesGcm, err := cipher.NewGCM(block)
	if err != nil {
		fmt.Printf("[Error] AES-GCM init failed: %v\n", err)
		return
	}

	iv := make([]byte, 12)
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		fmt.Printf("[Error] Failed to generate IV: %v\n", err)
		return
	}

	aesCiphertext := aesGcm.Seal(nil, iv, payload, nil)

	// 5. Create final bundle string
	bundle := fmt.Sprintf("%s:%s:%s",
		base64.StdEncoding.EncodeToString(kemCiphertext),
		base64.StdEncoding.EncodeToString(iv),
		base64.StdEncoding.EncodeToString(aesCiphertext),
	)

	namePrompt := promptui.Prompt{
		Label: "Message Name",
	}
	name, err := namePrompt.Run()
	if err == nil && name != "" {
		saveBundleToFile(name, bundle)
	}
}

func handleDecrypt(state *VaultState, bundleString string) {
	parts := strings.Split(strings.TrimSpace(bundleString), ":")
	if len(parts) != 3 {
		fmt.Println("[Error] Invalid bundle format. Expected 3 colon-separated fields.")
		return
	}

	kemCtBytes, _ := base64.StdEncoding.DecodeString(parts[0])
	ivBytes, _ := base64.StdEncoding.DecodeString(parts[1])
	aesCtBytes, _ := base64.StdEncoding.DecodeString(parts[2])

	// Decapsulate shared secret using internal ML-KEM-768 secret key
	kemSch := mlkem768.Scheme()
	sharedSecret, err := kemSch.Decapsulate(state.KemSecretKey, kemCtBytes)
	if err != nil {
		fmt.Printf("[Error] Decapsulation failed: %v\n", err)
		return
	}

	// Decrypt payload with AES-GCM
	block, err := aes.NewCipher(sharedSecret)
	if err != nil {
		fmt.Printf("[Error] AES decryption failed: %v\n", err)
		return
	}
	aesGcm, err := cipher.NewGCM(block)
	if err != nil {
		fmt.Printf("[Error] AES-GCM failed: %v\n", err)
		return
	}

	payload, err := aesGcm.Open(nil, ivBytes, aesCtBytes, nil)
	if err != nil {
		fmt.Printf("[Error] Decryption failed (bad key or corrupt bundle): %v\n", err)
		return
	}

	if len(payload) < 4 {
		fmt.Println("[Error] Malformed decrypted payload.")
		return
	}

	msgLen := int(payload[0])<<24 | int(payload[1])<<16 | int(payload[2])<<8 | int(payload[3])
	if len(payload) < 4+msgLen {
		fmt.Println("[Error] Malformed message length.")
		return
	}

	msgBytes := payload[4 : 4+msgLen]
	sigBytes := payload[4+msgLen:]
	recoveredMessage := string(msgBytes)

	// Verify internally using own ML-DSA-65 public key
	dsaSch := mldsa65.Scheme()
	valid := dsaSch.Verify(state.DsaPublicKey, msgBytes, sigBytes, nil)
	if !valid {
		fmt.Println("[Status] Signature INVALID — message is forged or corrupted.")
		return
	}

	fmt.Println("[Success] Signature verified.")
	fmt.Printf("Decrypted Message: %s\n", recoveredMessage)
}

func handleLoadBundleMenu(state *VaultState) {
	bundles, err := loadBundlesFromFile()
	if err != nil || len(bundles) == 0 {
		fmt.Printf("[Info] No saved bundles found in %s.\n", getBundleFilePath())
		return
	}

	items := make([]string, len(bundles))
	for i, b := range bundles {
		items[i] = b.Name
	}

	prompt := promptui.Select{
		Label: "Saved Messages (Use up/down arrow keys): ",
		Items: items,
	}

	idx, _, err := prompt.Run()
	if err != nil {
		return
	}

	selectedBundle := bundles[idx]
	fmt.Printf("[Loading] Selected bundle: %s\n", selectedBundle.Name)
	handleDecrypt(state, selectedBundle.Bundle)
}

func handleDeleteBundleMenu(state *VaultState) {
	bundles, err := loadBundlesFromFile()
	if err != nil || len(bundles) == 0 {
		fmt.Printf("[Info] No saved bundles found in %s.\n", getBundleFilePath())
		return
	}

	items := make([]string, len(bundles))
	for i, b := range bundles {
		items[i] = b.Name
	}

	prompt := promptui.Select{
		Label: "Select Message to Delete (Use up/down arrow keys): ",
		Items: items,
	}

	idx, _, err := prompt.Run()
	if err != nil {
		return
	}

	selectedName := bundles[idx].Name

	// Confirm deletion prompt
	confirmPrompt := promptui.Select{
		Label: fmt.Sprintf("Are you sure you want to delete '%s'?", selectedName),
		Items: []string{"Yes", "No"},
	}

	_, confirmChoice, err := confirmPrompt.Run()
	if err != nil || confirmChoice != "Yes" {
		fmt.Println("[Info] Deletion cancelled.")
		return
	}

	// Filter out the selected bundle
	var updatedBundles []BundleRecord
	for _, b := range bundles {
		if b.Name != selectedName {
			updatedBundles = append(updatedBundles, b)
		}
	}

	bundleFile := BundleFile{
		Format:  "pqc-vault-bundles",
		Version: 1,
		Bundles: updatedBundles,
	}

	filePath := getBundleFilePath()
	file, err := os.Create(filePath)
	if err != nil {
		fmt.Printf("[Error] Could not write %s: %v\n", filePath, err)
		return
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(bundleFile); err != nil {
		fmt.Printf("[Error] Failed to encode JSON: %v\n", err)
		return
	}
	fmt.Printf("[Success] Message '%s' deleted successfully from %s.\n", selectedName, filePath)
}

func loadBundlesFromFile() ([]BundleRecord, error) {
	filePath := getBundleFilePath()
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var bundleFile BundleFile
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&bundleFile); err != nil {
		file.Seek(0, 0)
		var rawList []BundleRecord
		if errArr := json.NewDecoder(file).Decode(&rawList); errArr != nil {
			return nil, err
		}
		return rawList, nil
	}

	return bundleFile.Bundles, nil
}

func saveBundleToFile(name, bundle string) {
	bundles, err := loadBundlesFromFile()
	if err == nil {
		bundles = append(bundles, BundleRecord{Name: name, Bundle: bundle})
	} else {
		bundles = []BundleRecord{{Name: name, Bundle: bundle}}
	}

	bundleFile := BundleFile{
		Format:  "pqc-vault-bundles",
		Version: 1,
		Bundles: bundles,
	}

	filePath := getBundleFilePath()
	file, err := os.Create(filePath)
	if err != nil {
		fmt.Printf("[Error] Could not write %s: %v\n", filePath, err)
		return
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(bundleFile); err != nil {
		fmt.Printf("[Error] Failed to encode JSON: %v\n", err)
		return
	}
	fmt.Printf("[Success] Saved bundle successfully into %s.\n", filePath)
}
