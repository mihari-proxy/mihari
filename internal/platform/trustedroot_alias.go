package platform

func trustedDarwinAlias(name, target string) bool {
	return (name == "var" || name == "tmp" || name == "etc") && (target == "private/"+name || target == "/private/"+name)
}
