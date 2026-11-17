//go:build !windows

package mediadownload

import "errors"

func privateSessionDirectory(string) error { return errors.New("站点连接目前仅支持 Windows") }
func unsealSession([]byte) ([]byte, error) {
	return nil, errors.New("站点连接目前仅支持 Windows")
}
func lockSession(string) (func(), error) {
	return nil, errors.New("站点连接目前仅支持 Windows")
}
func sessionProcessAlive(int) bool { return false }
