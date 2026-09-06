//go:build windows

package update

import "golang.org/x/sys/windows"

// Дескриптор завершившегося процесса ещё какое-то время открывается, поэтому
// проверяем именно состояние: иначе подмена файла ждёт лишние секунды.
func processAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	ev, err := windows.WaitForSingleObject(h, 0)
	return err == nil && ev == uint32(windows.WAIT_TIMEOUT)
}
