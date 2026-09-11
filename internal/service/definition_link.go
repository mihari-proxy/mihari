package service

import (
	"os"
	"path"
)

func checkedDefinitionLink(unitFile, devNull, target string) (string, error) {
	if target == "" {
		return "", os.ErrInvalid
	}
	if !allowedDefinitionLink(unitFile, devNull, target) {
		return "", os.ErrPermission
	}
	return target, nil
}

func allowedDefinitionLink(unitFile, devNull, target string) bool {
	if unitFile == "" {
		unitFile = defaultSystemdUnitFile
	}
	if devNull == "" {
		devNull = defaultDevNull
	}
	return unixAbs(target) && path.Clean(target) == target && (target == devNull || target == unitFile)
}
