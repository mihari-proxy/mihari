package service

import (
	"os"
	"path"
)

func checkedDefinitionLink(unitFile, target string) (string, error) {
	if target == "" {
		return "", os.ErrInvalid
	}
	if !allowedDefinitionLink(unitFile, target) {
		return "", os.ErrPermission
	}
	return target, nil
}

func allowedDefinitionLink(unitFile, target string) bool {
	if unitFile == "" {
		unitFile = defaultSystemdUnitFile
	}
	return unixAbs(target) && path.Clean(target) == target && (target == defaultDevNull || target == unitFile)
}
