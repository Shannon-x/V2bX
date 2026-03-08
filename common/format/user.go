package format

// UserTag builds a "tag|uuid" key for per-user lookups.
// Uses direct concatenation instead of fmt.Sprintf to avoid
// reflection overhead on every connection establishment.
func UserTag(tag string, uuid string) string {
	return tag + "|" + uuid
}
