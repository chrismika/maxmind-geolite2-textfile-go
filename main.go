package main

import (
	"archive/zip"
	"bufio"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	AccountID             string   `yaml:"account_id"`
	LicenseKey            string   `yaml:"license_key"`
	BlockedCountriesInput []string `yaml:"blocked_countries"`
	OutputFilePath        string   `yaml:"output_filepath"`
	OutputFilename        string   `yaml:"output_filename"`
	BlockedCountries      map[string]struct{}
}

const (
	dbURL               = "https://download.maxmind.com/geoip/databases/GeoLite2-Country-CSV/download?suffix=zip"
	shaURL              = "https://download.maxmind.com/geoip/databases/GeoLite2-Country-CSV/download?suffix=zip.sha256"
	geoLiteLocationsCSV = "GeoLite2-Country-Locations-en.csv"
	geoLiteBlocksCSV    = "GeoLite2-Country-Blocks-IPv4.csv"
)

var httpClient = &http.Client{
	Timeout: 30 * time.Second,
}

type stringSlice []string

func (s *stringSlice) String() string {
	return fmt.Sprintf("%v", *s)
}

func (s *stringSlice) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func parseCLIOptions() (*Config, string) {
	var blockedCountries stringSlice
	var configFilePath string
	cfg := &Config{
		BlockedCountries: make(map[string]struct{}),
	}

	flag.StringVar(&configFilePath, "c", "", "Config file")
	flag.StringVar(&cfg.AccountID, "id", "", "Account ID")
	flag.StringVar(&cfg.LicenseKey, "key", "", "License key")
	flag.StringVar(&cfg.OutputFilePath, "outpath", "", "Output path")
	flag.StringVar(&cfg.OutputFilename, "outname", "BlockedCountriesBlocks.txt", "Output file")
	flag.Var(&blockedCountries, "bc", "ISO Country codes to block (can be used multiple times)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n", os.Args[0])
		flag.PrintDefaults()
	}

	flag.Parse()

	for _, block := range blockedCountries {
		cfg.BlockedCountries[strings.ToUpper(block)] = struct{}{}
	}

	return cfg, configFilePath
}

func (cfg *Config) populateBlockedCountriesMap() {
	for _, countryCode := range cfg.BlockedCountriesInput {
		cfg.BlockedCountries[strings.ToUpper(countryCode)] = struct{}{}
	}
}

func loadConfigFile(configFilePath string) (*Config, error) {
	cfg := &Config{
		BlockedCountries: make(map[string]struct{}),
	}

	configFile, err := os.Open(configFilePath)
	if err != nil {
		return nil, fmt.Errorf("Error opening config file %s: %w", configFilePath, err)
	}
	defer configFile.Close()

	configFileYAML := yaml.NewDecoder(configFile)
	err = configFileYAML.Decode(cfg)
	if err != nil {
		return nil, fmt.Errorf("Error parsing config file %s: %w", configFilePath, err)
	}

	cfg.populateBlockedCountriesMap()

	normalizedBlockedCountries := make(map[string]struct{})
	for country := range cfg.BlockedCountries {
		normalizedBlockedCountries[strings.ToUpper(country)] = struct{}{}
	}
	cfg.BlockedCountries = normalizedBlockedCountries

	return cfg, nil
}

func loadConfig() (*Config, error) {
	cfg, configFilePath := parseCLIOptions()

	if configFilePath != "" {
		configFile, err := loadConfigFile(configFilePath)
		if err != nil {
			return nil, fmt.Errorf("Error loading config file %s: %w", configFilePath, err)
		}

		if cfg.AccountID == "" {
			cfg.AccountID = configFile.AccountID
		}
		if cfg.LicenseKey == "" {
			cfg.LicenseKey = configFile.LicenseKey
		}
		if cfg.OutputFilePath == "" {
			if configFile.OutputFilePath != "" {
				cfg.OutputFilePath = configFile.OutputFilePath
			}
		}
		if cfg.OutputFilename == "" {
			cfg.OutputFilename = configFile.OutputFilename
		}
		if len(cfg.BlockedCountries) == 0 {
			for countryCode := range configFile.BlockedCountries {
				cfg.BlockedCountries[countryCode] = struct{}{}
			}
		}
	}

	if cfg.AccountID == "" || cfg.LicenseKey == "" {
		flag.Usage()
		return nil, fmt.Errorf("Error: Account ID and License Key must be provided via CLI or config file")
	}

	return cfg, nil
}

func downloadZip(tmpDir string, cfg *Config) (string, error) {
	httpRequest, err := http.NewRequest("GET", dbURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create zip HTTP request: %w", err)
	}
	httpRequest.SetBasicAuth(cfg.AccountID, cfg.LicenseKey)

	httpResponse, err := httpClient.Do(httpRequest)
	if err != nil {
		return "", fmt.Errorf("zip fetch failed: %w", err)
	}
	defer httpResponse.Body.Close()

	if httpResponse.StatusCode != http.StatusOK {
		return "", fmt.Errorf("zip bad status: %s", httpResponse.Status)
	}

	const zipFilename = "db.zip"
	tmpZipPath := filepath.Join(tmpDir, zipFilename+".tmp")
	tmpZipFile, err := os.Create(tmpZipPath)
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}

	if _, err := io.Copy(tmpZipFile, httpResponse.Body); err != nil {
		tmpZipFile.Close()
		return "", fmt.Errorf("failed to write file: %w", err)
	}

	if err := tmpZipFile.Close(); err != nil {
		return "", fmt.Errorf("failed to close tmp file: %w", err)
	}

	zipPath := filepath.Join(tmpDir, zipFilename)
	if err := os.Rename(tmpZipPath, zipPath); err != nil {
		return "", fmt.Errorf("failed to rename temp file: %w", err)
	}

	return zipPath, nil
}

func verifySHA256(zipPath string, cfg *Config) error {
	httpRequest, err := http.NewRequest("GET", shaURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create sha HTTP request: %w", err)
	}
	httpRequest.SetBasicAuth(cfg.AccountID, cfg.LicenseKey)

	httpResponse, err := httpClient.Do(httpRequest)
	if err != nil {
		return fmt.Errorf("sha fetch failed: %w", err)
	}
	defer httpResponse.Body.Close()

	if httpResponse.StatusCode != http.StatusOK {
		return fmt.Errorf("sha bad status: %s", httpResponse.Status)
	}
	httpResonseBodyMaxRead := io.LimitReader(httpResponse.Body, 1024)
	shaData, err := io.ReadAll(httpResonseBodyMaxRead)
	if err != nil {
		return fmt.Errorf("failed to read sha data: %w", err)
	}

	shaParts := strings.Fields(string(shaData))
	if len(shaParts) == 0 {
		return fmt.Errorf("invalid sha file")
	}
	expectedSHA := shaParts[0]

	zipFile, err := os.Open(zipPath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer zipFile.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, zipFile); err != nil {
		return fmt.Errorf("failed to read file for sha256: %w", err)
	}

	actualSHA := hex.EncodeToString(hash.Sum(nil))

	if actualSHA != expectedSHA {
		return fmt.Errorf("sha256 mismatch: got %s, expected %s", actualSHA, expectedSHA)
	}

	return nil
}

func extractAndWriteFile(file *zip.File, destinationDir string) error {
	fileName := filepath.Base(file.Name)
	extractedFilePath := filepath.Join(destinationDir, fileName)

	zipFileContent, err := file.Open()
	if err != nil {
		return fmt.Errorf("failed to open file inside zip %s: %w", fileName, err)
	}
	defer zipFileContent.Close()

	extractedFile, err := os.Create(extractedFilePath)
	if err != nil {
		return fmt.Errorf("failed to create file %s: %w", extractedFilePath, err)
	}
	defer extractedFile.Close()

	_, err = io.Copy(extractedFile, zipFileContent)
	if err != nil {
		return fmt.Errorf("failed to write to file %s to %s: %w", fileName, extractedFilePath, err)
	}

	return nil
}

func extractZip(zipPath, tmpDir string) error {
	zipFile, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("failed to open zip file: %w", err)
	}
	defer zipFile.Close()

	filesToExtract := map[string]struct{}{
		geoLiteLocationsCSV: {},
		geoLiteBlocksCSV:    {},
	}

	foundCount := 0
	for _, file := range zipFile.File {
		if _, extract := filesToExtract[filepath.Base(file.Name)]; !extract {
			continue
		}

		foundCount++

		if err := extractAndWriteFile(file, tmpDir); err != nil {
			return err
		}
		if foundCount == len(filesToExtract) {
			break
		}
	}

	if foundCount < 2 {
		return fmt.Errorf("missing required files in zip archive")
	}

	return nil
}

func downloadGeolite2(tmpDir string, cfg *Config) error {
	zipPath, err := downloadZip(tmpDir, cfg)
	if err != nil {
		return err
	}

	if err := verifySHA256(zipPath, cfg); err != nil {
		return err
	}

	if err := extractZip(zipPath, tmpDir); err != nil {
		return err
	}

	return nil
}

func getGeonameIDs(tmpDir string, cfg *Config) (map[string]string, error) {
	locationsCSVPath := filepath.Join(tmpDir, geoLiteLocationsCSV)
	locationsCSVFile, err := os.Open(locationsCSVPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open %s: %w", geoLiteLocationsCSV, err)
	}
	defer locationsCSVFile.Close()

	csvData := csv.NewReader(locationsCSVFile)
	csvData.ReuseRecord = true
	csvHeader, err := csvData.Read()
	if err != nil {
		if err == io.EOF {
			return make(map[string]string), nil
		}
		return nil, fmt.Errorf("failed to read %s CSV header: %w", geoLiteLocationsCSV, err)
	}
	columns := make(map[string]int)
	for i, name := range csvHeader {
		columns[name] = i
	}
	neededFields := []string{"geoname_id", "country_iso_code"}
	for _, column := range neededFields {
		if _, ok := columns[column]; !ok {
			return nil, fmt.Errorf("missing needed column: %s", column)
		}
	}

	geonameIDsSet := make(map[string]string)

	for {
		line, err := csvData.Read()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("failed to read %s CSV line: %w", geoLiteLocationsCSV, err)
		}
		countryISOCode := strings.ToUpper(line[columns["country_iso_code"]])
		if _, isBlocked := cfg.BlockedCountries[countryISOCode]; isBlocked {
			geonameIDsSet[line[columns["geoname_id"]]] = countryISOCode
		}
	}
	return geonameIDsSet, nil
}

func getAndWriteBlocks(tmpDir string, geonameIDsSet map[string]string, cfg *Config) error {
	blocksCSVPath := filepath.Join(tmpDir, geoLiteBlocksCSV)
	blocksCSVFile, err := os.Open(blocksCSVPath)
	if err != nil {
		return fmt.Errorf("failed to open %s: %w", geoLiteBlocksCSV, err)
	}
	defer blocksCSVFile.Close()

	outputPath := filepath.Join(tmpDir, cfg.OutputFilename)
	outputFile, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file %s: %w", outputPath, err)
	}
	defer outputFile.Close()

	csvData := csv.NewReader(blocksCSVFile)
	csvData.ReuseRecord = true
	csvHeader, err := csvData.Read()
	if err != nil {
		if err == io.EOF {
			return nil
		}
		return fmt.Errorf("failed to read CSV header: %w", err)
	}
	columns := make(map[string]int)
	for i, name := range csvHeader {
		columns[name] = i
	}
	neededFields := []string{"network", "geoname_id", "registered_country_geoname_id", "represented_country_geoname_id"}
	for _, column := range neededFields {
		if _, ok := columns[column]; !ok {
			return fmt.Errorf("missing needed column: %s", column)
		}
	}
	targetIndices := []int{
		columns["geoname_id"],
		columns["registered_country_geoname_id"],
		columns["represented_country_geoname_id"],
	}
	networkIdx := columns["network"]

	outputData := bufio.NewWriter(outputFile)
	defer outputData.Flush()

	timestamp := time.Now().Format("2006/01/02-15:04")
	fmt.Fprintf(outputData, "# list generated %s\n", timestamp)

	for {
		line, err := csvData.Read()
		if err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("failed to read %s CSV line: %w", geoLiteBlocksCSV, err)
		}
		for _, index := range targetIndices {
			if country, found := geonameIDsSet[line[index]]; found {
				fmt.Fprintf(outputData, "%s ; %s\n", line[networkIdx], country)
				break
			}
		}
	}

	return nil
}

func moveFile(tmpDir string, cfg *Config) error {
	oldPath := filepath.Join(tmpDir, cfg.OutputFilename)
	newPath := filepath.Join(cfg.OutputFilePath, cfg.OutputFilename)
	if err := os.Rename(oldPath, newPath); err != nil {
		return fmt.Errorf("failed to rename file: %w", err)
	}
	return nil
}

func createTmpDir() (string, error) {
	tmpDir, err := os.MkdirTemp("", "")
	if err != nil {
		return "", fmt.Errorf("Failed to create temp directory: %w", err)
	}

	return tmpDir, nil
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}
	tmpDir, err := createTmpDir()
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)
	if err = downloadGeolite2(tmpDir, cfg); err != nil {
		log.Fatal(err)
	}
	geonameIDsSet, err := getGeonameIDs(tmpDir, cfg)
	if err != nil {
		log.Fatal(err)
	}
	if err = getAndWriteBlocks(tmpDir, geonameIDsSet, cfg); err != nil {
		log.Fatal(err)
	}
	if err = moveFile(tmpDir, cfg); err != nil {
		log.Fatal(err)
	}
	fmt.Println("Processing complete and file generated successfully.")
}
