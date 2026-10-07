package main

// Автозапуск через Планировщик заданий: только так программа стартует
// с правами администратора при входе в Windows без запроса UAC.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

const taskName = "Mejgorod"

func autostartEnabled() bool {
	cmd := exec.Command("schtasks", "/Query", "/TN", taskName)
	cmd.SysProcAttr = hiddenProc()
	return cmd.Run() == nil
}

func setAutostart(on bool, exe string) error {
	if !on {
		if !autostartEnabled() {
			return nil
		}
		cmd := exec.Command("schtasks", "/Delete", "/TN", taskName, "/F")
		cmd.SysProcAttr = hiddenProc()
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("schtasks /Delete: %v %s", err, strings.TrimSpace(decodeOEM(out)))
		}
		return nil
	}

	u, err := user.Current()
	if err != nil {
		return err
	}
	xml := fmt.Sprintf(taskXML, xmlEsc(u.Username), xmlEsc(u.Username), xmlEsc(exe), xmlEsc(filepath.Dir(exe)))
	f, err := os.CreateTemp("", "mihomodesk-task-*.xml")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	// schtasks ждёт XML в UTF-16 LE с BOM
	var buf bytes.Buffer
	buf.Write([]byte{0xFF, 0xFE})
	_ = binary.Write(&buf, binary.LittleEndian, utf16.Encode([]rune(xml)))
	if _, err := f.Write(buf.Bytes()); err != nil {
		f.Close()
		return err
	}
	f.Close()

	cmd := exec.Command("schtasks", "/Create", "/TN", taskName, "/XML", f.Name(), "/F")
	cmd.SysProcAttr = hiddenProc()
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("schtasks /Create: %v %s", err, strings.TrimSpace(decodeOEM(out)))
	}
	return nil
}

func xmlEsc(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return r.Replace(s)
}

// decodeOEM - вывод schtasks в кодировке cp866; для сообщения об ошибке
// достаточно выкинуть нечитаемое.
func decodeOEM(b []byte) string {
	var sb strings.Builder
	for _, c := range b {
		if c < 0x80 {
			sb.WriteByte(c)
		}
	}
	return sb.String()
}

// ExecutionTimeLimit PT0S - иначе Windows убьёт программу через 72 часа.
// Батарея не мешает запуску и не останавливает задачу.
const taskXML = `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>Mejgorod: VPN при входе в Windows</Description>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>
      <UserId>%s</UserId>
      <Delay>PT5S</Delay>
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <UserId>%s</UserId>
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>HighestAvailable</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>false</StartWhenAvailable>
    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>
    <IdleSettings>
      <StopOnIdleEnd>false</StopOnIdleEnd>
      <RestartOnIdle>false</RestartOnIdle>
    </IdleSettings>
    <AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>true</Enabled>
    <Hidden>false</Hidden>
    <RunOnlyIfIdle>false</RunOnlyIfIdle>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Priority>7</Priority>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>%s</Command>
      <Arguments>--autostart</Arguments>
      <WorkingDirectory>%s</WorkingDirectory>
    </Exec>
  </Actions>
</Task>
`
