package subscription

import (
	"strconv"
	"strings"
)

// UserInfo is traffic/quota metadata from a provider's subscription-userinfo header.
// Values are bytes unless noted.
type UserInfo struct {
	Upload   int64
	Download int64
	Total    int64
	Expire   int64 // unix seconds; 0 if unknown
}

// Used returns upload+download traffic, clamped at zero.
func (u UserInfo) Used() int64 {
	used := u.Upload + u.Download
	if used < 0 {
		return 0
	}
	return used
}

// ParseUserInfo parses headers like:
//
//	upload=123; download=456; total=789; expire=1710000000
func ParseUserInfo(raw string) (UserInfo, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return UserInfo{}, false
	}
	var info UserInfo
	found := false
	for _, part := range strings.Split(raw, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n < 0 {
			continue
		}
		switch key {
		case "upload":
			info.Upload = n
			found = true
		case "download":
			info.Download = n
			found = true
		case "total":
			info.Total = n
			found = true
		case "expire":
			info.Expire = n
			found = true
		}
	}
	return info, found
}
