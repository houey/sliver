package generate

/*
	Sliver Implant Framework
	Copyright (C) 2019  Bishop Fox

	This program is free software: you can redistribute it and/or modify
	it under the terms of the GNU General Public License as published by
	the Free Software Foundation, either version 3 of the License, or
	(at your option) any later version.

	This program is distributed in the hope that it will be useful,
	but WITHOUT ANY WARRANTY; without even the implied warranty of
	MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
	GNU General Public License for more details.

	You should have received a copy of the GNU General Public License
	along with this program.  If not, see <https://www.gnu.org/licenses/>.
*/

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"text/template"

	"github.com/bishopfox/sliver/implant"
	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/server/assets"
	"github.com/bishopfox/sliver/server/certs"
	"github.com/bishopfox/sliver/server/db/models"
	"github.com/bishopfox/sliver/server/gogo"
	"github.com/bishopfox/sliver/server/log"
	"github.com/bishopfox/sliver/util"
)

var (
	buildLog = log.NamedLogger("generate", "build")

	// RUNTIME GOOS -> TARGET GOOS -> TARGET ARCH
	defaultCCPaths = map[string]map[string]map[string]string{
		"linux": {
			"windows": {
				"386":   "/usr/bin/i686-w64-mingw32-gcc",
				"amd64": "/usr/bin/x86_64-w64-mingw32-gcc",
			},
			"darwin": {
				"amd64": "/opt/osxcross/target/bin/o64-clang", // OSX Cross
				"arm64": "/opt/osxcross/target/bin/aarch64-apple-darwin20.2-clang",
			},
		},
		"darwin": {
			"windows": {
				"386":   "/usr/bin/i686-w64-mingw32-gcc",
				"amd64": "/usr/bin/x86_64-w64-mingw32-gcc",
			},
		},
	}

	// SupportedCompilerTargets - Supported compiler targets
	SupportedCompilerTargets = map[string]bool{
		"darwin/amd64":  true,
		"darwin/arm64":  true,
		"linux/386":     true,
		"linux/amd64":   true,
		"windows/386":   true,
		"windows/amd64": true,
	}
)

const (
	// WINDOWS OS
	WINDOWS = "windows"

	// DARWIN / MacOS
	DARWIN = "darwin"

	// LINUX OS
	LINUX = "linux"

	// GoPrivate - The default Go private arg to garble when obfuscation is enabled.
	// Wireguard dependencies prevent the use of wildcard github.com/* and golang.org/*.
	// The current packages below aren't definitive and need to be tidied up.
	GoPrivate = "github.com/bishopfox/*,github.com/Microsoft/*,github.com/burntsushi/*,github.com/kbinani/*,github.com/lxn/*,github.com/golang/*,github.com/shm/*,github.com/lesnuages/*"

	clientsDirName = "clients"
	sliversDirName = "slivers"

	encryptKeySize = 16

	// DefaultReconnectInterval - In seconds
	DefaultReconnectInterval = 60
	// DefaultMTLSLPort - Default listen port
	DefaultMTLSLPort = 8888
	// DefaultHTTPLPort - Default HTTP listen port
	DefaultHTTPLPort = 443 // Assume SSL, it'll fallback
	// DefaultPollInterval - In seconds
	DefaultPollInterval = 1

	// DefaultSuffix - Indicates a platform independent src file
	DefaultSuffix = "_default.go"

	// *** Default ***
	// SliverCC64EnvVar - Environment variable that can specify the 64 bit mingw path
	SliverCC64EnvVar = "SLIVER_CC_64"
	// SliverCC32EnvVar - Environment variable that can specify the 32 bit mingw path
	SliverCC32EnvVar = "SLIVER_CC_32"

	// SliverCXX64EnvVar - Environment variable that can specify the 64 bit mingw path
	SliverCXX64EnvVar = "SLIVER_CXX_64"
	// SliverCXX32EnvVar - Environment variable that can specify the 32 bit mingw path
	SliverCXX32EnvVar = "SLIVER_CXX_32"

	// *** Platform Specific ***
	// SliverPlatformCC64EnvVar - Environment variable that can specify the 64 bit mingw path
	SliverPlatformCC64EnvVar = "SLIVER_%s_CC_64"
	// SliverPlatformCC32EnvVar - Environment variable that can specify the 32 bit mingw path
	SliverPlatformCC32EnvVar = "SLIVER_%s_CC_32"
	// SliverPlatformCXX64EnvVar - Environment variable that can specify the 64 bit mingw path
	SliverPlatformCXX64EnvVar = "SLIVER_%s_CXX_64"
	// SliverPlatformCXX32EnvVar - Environment variable that can specify the 32 bit mingw path
	SliverPlatformCXX32EnvVar = "SLIVER_%s_CXX_32"
)

// ImplantConfigFromProtobuf - Create a native config struct from Protobuf
func ImplantConfigFromProtobuf(pbConfig *clientpb.ImplantConfig) (string, *models.ImplantConfig) {
	cfg := &models.ImplantConfig{}

	cfg.GOOS = pbConfig.GOOS
	cfg.GOARCH = pbConfig.GOARCH
	cfg.CACert = pbConfig.CACert
	cfg.Cert = pbConfig.Cert
	cfg.Key = pbConfig.Key
	cfg.Debug = pbConfig.Debug
	cfg.Evasion = pbConfig.Evasion
	cfg.ObfuscateSymbols = pbConfig.ObfuscateSymbols
	// cfg.CanaryDomains = pbConfig.CanaryDomains

	cfg.WGImplantPrivKey = pbConfig.WGImplantPrivKey
	cfg.WGServerPubKey = pbConfig.WGServerPubKey
	cfg.WGPeerTunIP = pbConfig.WGPeerTunIP
	cfg.WGKeyExchangePort = pbConfig.WGKeyExchangePort
	cfg.WGTcpCommsPort = pbConfig.WGTcpCommsPort
	cfg.ReconnectInterval = pbConfig.ReconnectInterval
	cfg.MaxConnectionErrors = pbConfig.MaxConnectionErrors
	cfg.PollInterval = pbConfig.PollInterval

	cfg.LimitDomainJoined = pbConfig.LimitDomainJoined
	cfg.LimitDatetime = pbConfig.LimitDatetime
	cfg.LimitUsername = pbConfig.LimitUsername
	cfg.LimitHostname = pbConfig.LimitHostname
	cfg.LimitFileExists = pbConfig.LimitFileExists

	cfg.Format = pbConfig.Format
	cfg.IsSharedLib = pbConfig.IsSharedLib
	cfg.IsService = pbConfig.IsService
	cfg.IsShellcode = pbConfig.IsShellcode

	cfg.CanaryDomains = []models.CanaryDomain{}
	for _, pbCanary := range pbConfig.CanaryDomains {
		cfg.CanaryDomains = append(cfg.CanaryDomains, models.CanaryDomain{
			Domain: pbCanary,
		})
	}

	// Copy C2
	cfg.C2 = copyC2List(pbConfig.C2)
	cfg.MTLSc2Enabled = isC2Enabled([]string{"mtls"}, cfg.C2)
	cfg.WGc2Enabled = isC2Enabled([]string{"wg"}, cfg.C2)
	cfg.HTTPc2Enabled = isC2Enabled([]string{"http", "https"}, cfg.C2)
	cfg.DNSc2Enabled = isC2Enabled([]string{"dns"}, cfg.C2)
	cfg.NamePipec2Enabled = isC2Enabled([]string{"namedpipe"}, cfg.C2)
	cfg.TCPPivotc2Enabled = isC2Enabled([]string{"tcppivot"}, cfg.C2)

	if pbConfig.FileName != "" {
		cfg.FileName = path.Base(pbConfig.FileName)
	}

	name := ""
	if pbConfig.Name != "" {
		// Only allow user-provided alpha/numeric names
		if regexp.MustCompile(`^[[:alnum:]]+$`).MatchString(pbConfig.Name) {
			name = pbConfig.Name
		}
	}
	return name, cfg
}

func copyC2List(src []*clientpb.ImplantC2) []models.ImplantC2 {
	c2s := []models.ImplantC2{}
	for _, srcC2 := range src {
		c2URL, err := url.Parse(srcC2.URL)
		if err != nil {
			buildLog.Warnf("Failed to parse c2 url %v", err)
			continue
		}
		c2s = append(c2s, models.ImplantC2{
			Priority: srcC2.Priority,
			URL:      c2URL.String(),
			Options:  srcC2.Options,
		})
	}
	return c2s
}

func isC2Enabled(schemes []string, c2s []models.ImplantC2) bool {
	for _, c2 := range c2s {
		c2URL, err := url.Parse(c2.URL)
		if err != nil {
			buildLog.Warnf("Failed to parse c2 url %v", err)
			continue
		}
		for _, scheme := range schemes {
			if scheme == c2URL.Scheme {
				return true
			}
		}
	}
	buildLog.Debugf("No %v URLs found in %v", schemes, c2s)
	return false
}

// GetSliversDir - Get the binary directory
func GetSliversDir() string {
	appDir := assets.GetRootAppDir()
	sliversDir := path.Join(appDir, sliversDirName)
	if _, err := os.Stat(sliversDir); os.IsNotExist(err) {
		buildLog.Infof("Creating bin directory: %s", sliversDir)
		err = os.MkdirAll(sliversDir, 0700)
		if err != nil {
			buildLog.Fatal(err)
		}
	}
	return sliversDir
}

// -----------------------
// Sliver Generation Code
// -----------------------

// SliverShellcode - Generates a sliver shellcode using sRDI
func SliverShellcode(name string, config *models.ImplantConfig) (string, error) {
	// Compile go code
	// Compile go code
	var cc string
	var cxx string

	appDir := assets.GetRootAppDir()
	// Don't use a cross-compiler if the target bin is built on the same platform
	// as the sliver-server.
	if runtime.GOOS != config.GOOS {
		buildLog.Infof("Cross-compiling from %s/%s to %s/%s", runtime.GOOS, runtime.GOARCH, config.GOOS, config.GOARCH)
		cc, cxx = getCrossCompilers(config.GOOS, config.GOARCH)
		if cc == "" {
			return "", fmt.Errorf("CC '%s/%s' not found", config.GOOS, config.GOARCH)
		}
	}
	goConfig := &gogo.GoConfig{
		CGO: "1",
		CC:  cc,
		CXX: cxx,

		GOOS:       config.GOOS,
		GOARCH:     config.GOARCH,
		GOCACHE:    gogo.GetGoCache(appDir),
		GOMODCACHE: gogo.GetGoModCache(appDir),
		GOROOT:     gogo.GetGoRootDir(appDir),

		Obfuscation: config.ObfuscateSymbols,
		GOPRIVATE:   GoPrivate,
	}
	pkgPath, err := renderSliverGoCode(name, config, goConfig)
	if err != nil {
		return "", err
	}

	dest := path.Join(goConfig.ProjectDir, "bin", path.Base(name))
	dest += ".bin"

	tags := []string{"netgo"}
	ldflags := []string{"-s -w -buildid="}
	if !config.Debug && goConfig.GOOS == WINDOWS {
		ldflags[0] += " -H=windowsgui"
	}
	// Keep those for potential later use
	gcflags := fmt.Sprintf("")
	asmflags := fmt.Sprintf("")
	// trimpath is now a separate flag since Go 1.13
	trimpath := "-trimpath"
	_, err = gogo.GoBuild(*goConfig, pkgPath, dest, "pie", tags, ldflags, gcflags, asmflags, trimpath)
	config.FileName = path.Base(dest)
	shellcode, err := DonutShellcodeFromFile(dest, config.GOARCH, false, "", "", "")

	if err != nil {
		return "", err
	}
	err = ioutil.WriteFile(dest, shellcode, 0600)
	if err != nil {
		return "", err
	}
	config.Format = clientpb.ImplantConfig_SHELLCODE
	// Save to database
	saveBuildErr := ImplantBuildSave(name, config, dest)
	if saveBuildErr != nil {
		buildLog.Errorf("Failed to save build: %s", saveBuildErr)
	}
	return dest, err

}

// SliverSharedLibrary - Generates a sliver shared library (DLL/dylib/so) binary
func SliverSharedLibrary(name string, config *models.ImplantConfig) (string, error) {
	// Compile go code
	var cc string
	var cxx string

	appDir := assets.GetRootAppDir()
	// Don't use a cross-compiler if the target bin is built on the same platform
	// as the sliver-server.
	if runtime.GOOS != config.GOOS {
		buildLog.Infof("Cross-compiling from %s/%s to %s/%s", runtime.GOOS, runtime.GOARCH, config.GOOS, config.GOARCH)
		cc, cxx = getCrossCompilers(config.GOOS, config.GOARCH)
		if cc == "" {
			return "", fmt.Errorf("CC '%s/%s' not found", config.GOOS, config.GOARCH)
		}
	}
	goConfig := &gogo.GoConfig{
		CGO: "1",
		CC:  cc,
		CXX: cxx,

		GOOS:       config.GOOS,
		GOARCH:     config.GOARCH,
		GOCACHE:    gogo.GetGoCache(appDir),
		GOMODCACHE: gogo.GetGoModCache(appDir),
		GOROOT:     gogo.GetGoRootDir(appDir),

		Obfuscation: config.ObfuscateSymbols,
		GOPRIVATE:   GoPrivate,
	}
	pkgPath, err := renderSliverGoCode(name, config, goConfig)
	if err != nil {
		return "", err
	}

	dest := path.Join(goConfig.ProjectDir, "bin", path.Base(name))
	if goConfig.GOOS == WINDOWS {
		dest += ".dll"
	}
	if goConfig.GOOS == DARWIN {
		dest += ".dylib"
	}
	if goConfig.GOOS == LINUX {
		dest += ".so"
	}

	tags := []string{"netgo"}
	ldflags := []string{"-s -w -buildid="}
	if !config.Debug && goConfig.GOOS == WINDOWS {
		ldflags[0] += " -H=windowsgui"
	}
	// Keep those for potential later use
	gcflags := fmt.Sprintf("")
	asmflags := fmt.Sprintf("")
	// trimpath is now a separate flag since Go 1.13
	trimpath := "-trimpath"
	_, err = gogo.GoBuild(*goConfig, pkgPath, dest, "c-shared", tags, ldflags, gcflags, asmflags, trimpath)
	config.FileName = path.Base(dest)

	err = ImplantBuildSave(name, config, dest)
	if err != nil {
		buildLog.Errorf("Failed to save build: %s", err)
	}
	return dest, err
}

// SliverExecutable - Generates a sliver executable binary
func SliverExecutable(name string, config *models.ImplantConfig) (string, error) {
	// Compile go code
	appDir := assets.GetRootAppDir()
	cgo := "0"
	if config.IsSharedLib {
		cgo = "1"
	}

	goConfig := &gogo.GoConfig{
		CGO:        cgo,
		GOOS:       config.GOOS,
		GOARCH:     config.GOARCH,
		GOROOT:     gogo.GetGoRootDir(appDir),
		GOCACHE:    gogo.GetGoCache(appDir),
		GOMODCACHE: gogo.GetGoModCache(appDir),

		Obfuscation: config.ObfuscateSymbols,
		GOPRIVATE:   GoPrivate,
	}

	pkgPath, err := renderSliverGoCode(name, config, goConfig)
	if err != nil {
		return "", err
	}

	dest := path.Join(goConfig.ProjectDir, "bin", path.Base(name))
	if goConfig.GOOS == WINDOWS {
		dest += ".exe"
	}
	tags := []string{"netgo"}
	ldflags := []string{"-s -w -buildid="}
	if !config.Debug && goConfig.GOOS == WINDOWS {
		ldflags[0] += " -H=windowsgui"
	}
	gcflags := fmt.Sprintf("")
	asmflags := fmt.Sprintf("")
	if config.Debug {
		gcflags = "all=-N -l"
		ldflags = []string{}
	}
	// trimpath is now a separate flag since Go 1.13
	trimpath := ""
	if !config.Debug {
		trimpath = "-trimpath"
	}
	_, err = gogo.GoBuild(*goConfig, pkgPath, dest, "", tags, ldflags, gcflags, asmflags, trimpath)
	config.FileName = path.Base(dest)

	err = ImplantBuildSave(name, config, dest)

	if err != nil {
		buildLog.Errorf("Failed to save build: %s", err)
	}
	return dest, err
}

// This function is a little too long, we should probably refactor it as some point
func renderSliverGoCode(name string, config *models.ImplantConfig, goConfig *gogo.GoConfig) (string, error) {
	var err error
	target := fmt.Sprintf("%s/%s", config.GOOS, config.GOARCH)
	if _, ok := gogo.ValidCompilerTargets[target]; !ok {
		return "", fmt.Errorf("Invalid compiler target: %s", target)
	}

	buildLog.Infof("Generating new sliver binary '%s'", name)

	config.MTLSc2Enabled = isC2Enabled([]string{"mtls"}, config.C2)
	config.WGc2Enabled = isC2Enabled([]string{"wg"}, config.C2)
	config.HTTPc2Enabled = isC2Enabled([]string{"http", "https"}, config.C2)
	config.DNSc2Enabled = isC2Enabled([]string{"dns"}, config.C2)
	config.NamePipec2Enabled = isC2Enabled([]string{"namedpipe"}, config.C2)
	config.TCPPivotc2Enabled = isC2Enabled([]string{"tcppivot"}, config.C2)

	sliversDir := GetSliversDir() // ~/.sliver/slivers
	projectGoPathDir := path.Join(sliversDir, config.GOOS, config.GOARCH, path.Base(name))
	if _, err := os.Stat(projectGoPathDir); os.IsNotExist(err) {
		os.MkdirAll(projectGoPathDir, 0700)
	}

	goConfig.ProjectDir = projectGoPathDir

	// Cert PEM encoded certificates
	serverCACert, _, _ := certs.GetCertificateAuthorityPEM(certs.C2ServerCA)
	sliverCert, sliverKey, err := certs.ImplantGenerateECCCertificate(name)
	if err != nil {
		return "", err
	}
	config.CACert = string(serverCACert)
	config.Cert = string(sliverCert)
	config.Key = string(sliverKey)

	// Generate wg Keys as needed
	if config.WGc2Enabled {
		implantPrivKey, _, err := certs.ImplantGenerateWGKeys(config.WGPeerTunIP)
		_, serverPubKey, err := certs.GetWGServerKeys()

		if err != nil {
			return "", fmt.Errorf("Failed to embed implant wg keys: %s", err)
		} else {
			config.WGImplantPrivKey = implantPrivKey
			config.WGServerPubKey = serverPubKey
		}
	}

	// binDir - ~/.sliver/slivers/<os>/<arch>/<name>/bin
	binDir := path.Join(projectGoPathDir, "bin")
	os.MkdirAll(binDir, 0700)

	// srcDir - ~/.sliver/slivers/<os>/<arch>/<name>/src
	srcDir := path.Join(projectGoPathDir, "src")
	assets.SetupGoPath(srcDir)            // Extract GOPATH dependency files
	err = util.ChmodR(srcDir, 0600, 0700) // Ensures src code files are writable
	if err != nil {
		buildLog.Errorf("fs perms: %v", err)
		return "", err
	}

	sliverPkgDir := path.Join(srcDir, "github.com", "bishopfox", "sliver") // "main"
	err = os.MkdirAll(sliverPkgDir, 0700)
	if err != nil {
		return "", nil
	}

	// Load code template
	renderFiles := srcFiles
	_, isSupportedTarget := SupportedCompilerTargets[fmt.Sprintf("%s/%s", config.GOOS, config.GOARCH)]
	if !isSupportedTarget {
		buildLog.Warnf("Unsupported compiler target, using generic src files ...")
		renderFiles = genericSrcFiles
	}
	for index, boxName := range renderFiles {

		// Gobfuscate doesn't handle all the platform specific code
		// well and the renamer can get confused when symbols for a
		// different OS don't show up. So we just filter out anything
		// we're not actually going to compile into the final binary
		suffix := ".go"
		if strings.Contains(boxName, "_") {
			fileNameParts := strings.Split(boxName, "_")
			suffix = "_" + fileNameParts[len(fileNameParts)-1]

			// Test files get skipped
			if strings.HasSuffix(boxName, "_test.go") {
				buildLog.Infof("Skipping (test): %s", boxName)
				continue
			}

			// We only include "_default.go" files for "unsupported" platforms i.e., not windows/darwin/linux
			if suffix == DefaultSuffix && isSupportedTarget {
				buildLog.Infof("Skipping default file (target is supported): %s", boxName)
				continue
			}

			// Only include code for our target goos/goarch
			if isSupportedTarget {
				osSuffix := fmt.Sprintf("_%s.go", strings.ToLower(config.GOOS))
				archSuffix := fmt.Sprintf("_%s.go", strings.ToLower(config.GOARCH))
				if !strings.HasSuffix(boxName, osSuffix) && !strings.HasSuffix(boxName, archSuffix) {
					buildLog.Infof("Skipping file wrong os/arch: %s", boxName)
					continue
				}
			}
		}

		sliverGoCodeRaw, err := implant.FS.ReadFile(path.Join("sliver", boxName))
		if err != nil {
			buildLog.Warnf("Failed to read %s: %s", boxName, err)
			continue
		}
		sliverGoCode := string(sliverGoCodeRaw)

		// We need to correct for the "github.com/bishopfox/sliver/implant/sliver/foo" imports,
		// since Go doesn't allow relative imports and "sliver" is a subdirectory of
		// the main "sliver" repo we need to fake this when copying the code
		// to our per-compile "GOPATH"
		var sliverCodePath string
		dirName := filepath.Dir(boxName)
		var fileName string
		// Skip dllmain files for anything non windows
		if boxName == "sliver.h" || boxName == "sliver.c" {
			if !config.IsSharedLib && !config.IsShellcode {
				continue
			}
		}
		if config.Debug || strings.HasSuffix(boxName, ".c") || strings.HasSuffix(boxName, ".h") {
			fileName = filepath.Base(boxName)
		} else {
			fileName = fmt.Sprintf("s%d%s", index, suffix)
		}
		if dirName != "." {
			// Add an extra "sliver" dir
			dirPath := path.Join(sliverPkgDir, "implant", "sliver", dirName)
			if _, err := os.Stat(dirPath); os.IsNotExist(err) {
				buildLog.Infof("[mkdir] %#v", dirPath)
				err = os.MkdirAll(dirPath, 0700)
				if err != nil {
					return "", err
				}
			}
			sliverCodePath = path.Join(dirPath, fileName)
		} else {
			sliverCodePath = path.Join(sliverPkgDir, fileName)
		}

		fSliver, err := os.Create(sliverCodePath)
		if err != nil {
			return "", err
		}
		buf := bytes.NewBuffer([]byte{})
		buildLog.Infof("[render] %s -> %s", boxName, sliverCodePath)

		// Render code
		sliverCodeTmpl := template.Must(template.New("sliver").Parse(sliverGoCode))
		err = sliverCodeTmpl.Execute(buf, struct {
			Name   string
			Config *models.ImplantConfig
		}{
			name,
			config,
		})
		if err != nil {
			buildLog.Error(err)
			return "", err
		}

		// Render canaries
		buildLog.Infof("Canary domain(s): %v", config.CanaryDomains)
		canaryTmpl := template.New("canary").Delims("[[", "]]")
		canaryGenerator := &CanaryGenerator{
			ImplantName:   name,
			ParentDomains: config.CanaryDomainsList(),
		}
		canaryTmpl, err = canaryTmpl.Funcs(template.FuncMap{
			"GenerateCanary": canaryGenerator.GenerateCanary,
		}).Parse(buf.String())
		if err != nil {
			return "", err
		}
		err = canaryTmpl.Execute(fSliver, canaryGenerator)

		if err != nil {
			buildLog.Infof("Failed to render go code: %s", err)
			return "", err
		}
	}

	// Render GoMod
	buildLog.Info("Rendering go.mod file ...")
	goModPath := path.Join(sliverPkgDir, "go.mod")
	err = ioutil.WriteFile(goModPath, []byte(implant.GoMod), 0600)
	if err != nil {
		return "", err
	}
	goSumPath := path.Join(sliverPkgDir, "go.sum")
	err = ioutil.WriteFile(goSumPath, []byte(implant.GoSum), 0600)
	if err != nil {
		return "", err
	}
	buildLog.Infof("Created %s", goModPath)
	output, err := gogo.GoMod((*goConfig), sliverPkgDir, []string{"tidy"})
	if err != nil {
		buildLog.Errorf("Go mod tidy failed:\n%s", output)
		return "", err
	}

	if err != nil {
		buildLog.Errorf("Failed to save sliver config %s", err)
		return "", err
	}
	return sliverPkgDir, nil
}

// Platform specific ENV VARS take precedence over generic
func getCrossCompilersFromEnv(targetGoos string, targetGoarch string) (string, string) {
	var cc string
	var cxx string

	TARGET_GOOS := strings.ToUpper(targetGoos)

	// Get Defaults
	if targetGoarch == "amd64" {
		cc = os.Getenv(SliverCC64EnvVar)
		if os.Getenv(fmt.Sprintf(SliverPlatformCC64EnvVar, TARGET_GOOS)) != "" {
			cc = os.Getenv(fmt.Sprintf(SliverPlatformCC64EnvVar, TARGET_GOOS))
		}
		cxx = os.Getenv(SliverCXX64EnvVar)
		if os.Getenv(fmt.Sprintf(SliverPlatformCXX64EnvVar, TARGET_GOOS)) != "" {
			cc = os.Getenv(fmt.Sprintf(SliverPlatformCXX64EnvVar, TARGET_GOOS))
		}
	}
	if targetGoarch == "386" {
		cc = os.Getenv(SliverCC32EnvVar)
		if os.Getenv(fmt.Sprintf(SliverPlatformCC32EnvVar, TARGET_GOOS)) != "" {
			cc = os.Getenv(fmt.Sprintf(SliverPlatformCC32EnvVar, TARGET_GOOS))
		}
		cxx = os.Getenv(SliverCXX64EnvVar)
		if os.Getenv(fmt.Sprintf(SliverPlatformCXX32EnvVar, TARGET_GOOS)) != "" {
			cc = os.Getenv(fmt.Sprintf(SliverPlatformCXX32EnvVar, TARGET_GOOS))
		}
	}
	return cc, cxx
}

func getCrossCompilers(targetGoos string, targetGoarch string) (string, string) {
	var found bool // meh, ugly

	// Get CC and CXX from ENV
	cc, cxx := getCrossCompilersFromEnv(targetGoos, targetGoarch)

	// If no CC is set in ENV then look for default path(s), we need a CC
	// but don't always need a CXX so we only WARN on a missing CXX
	if cc == "" {
		buildLog.Info("CC not found in ENV, using default paths")
		if _, ok := defaultCCPaths[runtime.GOOS]; ok {
			if cc, found = defaultCCPaths[runtime.GOOS][targetGoos][targetGoarch]; !found {
				buildLog.Warnf("No default for %s/%s from %s", targetGoos, targetGoarch, runtime.GOOS)
			}
		} else {
			buildLog.Warnf("No default paths for %s runtime", runtime.GOOS)
		}
	}

	// Check to see if CC and CXX exist
	if _, err := os.Stat(cc); os.IsNotExist(err) {
		buildLog.Warnf("CC path '%s' does not exist", cc)
	}
	if _, err := os.Stat(cxx); os.IsNotExist(err) {
		buildLog.Warnf("CXX path '%s' does not exist", cxx)
	}
	buildLog.Infof(" CC = '%s'", cc)
	buildLog.Infof("CXX = '%s'", cxx)
	return cc, cxx
}
