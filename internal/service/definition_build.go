package service

import (
	"bytes"
	"encoding/xml"
	"github.com/mihari-proxy/mihari/internal/platform"
	"path"
	"strconv"
	"strings"
)

// BuildUnixDefinition returns the absolute, fixed-environment managed definition.
func BuildUnixDefinition(layout platform.ResolvedLayout, goos string) (Definition, error) {
	if layout.Mode != platform.SystemMode && layout.Mode != platform.PrivateMode {
		return Definition{}, invalidServiceState("invalid service layout")
	}
	def := Definition{Status: StatusStopped, Binary: path.Join(layout.InstallRoot, "mihari"), Args: []string{"daemon", "--system-service"}, Enabled: true}
	def.Env = []string{"MIHARI_CONTROL_ENDPOINT=" + layout.ControlEndpoint, "MIHARI_CONTROL_CREDENTIAL=" + layout.CredentialPath, "MIHARI_INSTALL_ROOT=" + layout.InstallRoot}
	if layout.Mode == platform.PrivateMode {
		def.Env = append(def.Env, "MIHARI_DATA="+layout.Data.Root)
	}
	for _, value := range append([]string{def.Binary}, def.Env...) {
		if strings.ContainsAny(value, "\x00\n\r$%") {
			return Definition{}, invalidServiceState("unsupported service path")
		}
	}
	var body strings.Builder
	file := DefinitionFile{Owner: 0, Mode: 0644}
	switch goos {
	case "linux":
		file.Path, file.Kind = defaultSystemdUnitFile, "unit"
		body.WriteString("[Unit]\nDescription=Mihari\nAfter=network.target\n\n[Service]\nType=simple\nExecStart=" + strconv.Quote(def.Binary) + " daemon --system-service\n")
		for _, env := range def.Env {
			body.WriteString("Environment=" + strconv.Quote(env) + "\n")
		}
		body.WriteString("Restart=on-failure\nKillMode=control-group\n\n[Install]\nWantedBy=multi-user.target\n")
	case "darwin":
		file.Path, file.Kind = defaultPlistPath, "plist"
		escape := func(value string) string {
			var b bytes.Buffer
			_ = xml.EscapeText(&b, []byte(value))
			return b.String()
		} // bytes.Buffer cannot fail.
		body.WriteString(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>Label</key><string>mihari</string><key>ProgramArguments</key><array><string>` + escape(def.Binary) + `</string><string>daemon</string><string>--system-service</string></array><key>EnvironmentVariables</key><dict>`)
		for _, env := range def.Env {
			key, value, _ := strings.Cut(env, "=")
			body.WriteString("<key>" + escape(key) + "</key><string>" + escape(value) + "</string>")
		}
		body.WriteString("</dict><key>RunAtLoad</key><true/><key>KeepAlive</key><true/></dict></plist>")
	default:
		return Definition{}, invalidServiceState("unsupported service manager")
	}
	file.Bytes = []byte(body.String())
	def.Files = []DefinitionFile{file}
	return def, nil
}
