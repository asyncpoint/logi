package engine

// FieldValue is one structured field retained with a LogRecord.
// Value is the display/search representation. Raw distinguishes JSON values
// that were not JSON strings from ordinary string fields.
type FieldValue struct {
	Key   string
	Value string
	Raw   bool
}

// Fields is a compact, allocation-friendly collection of structured fields.
// Log records normally contain only a handful of fields, so a slice avoids the
// bucket/entry/interface overhead of map[string]any while preserving lookup
// semantics through Get and Set.
type Fields []FieldValue

// Get returns a field by name. Field names are case-sensitive.
func (f Fields) Get(key string) (FieldValue, bool) {
	for i := range f {
		if f[i].Key == key {
			return f[i], true
		}
	}
	return FieldValue{}, false
}

// Set adds or replaces a field. It is optimized for the small field counts
// typical of log records and deliberately avoids a secondary index.
func (f *Fields) Set(key, value string, raw bool) {
	if f == nil {
		return
	}
	for i := range *f {
		if (*f)[i].Key == key {
			(*f)[i].Value = value
			(*f)[i].Raw = raw
			return
		}
	}
	*f = append(*f, FieldValue{Key: key, Value: value, Raw: raw})
}

// GetValue returns the display/search value for a field.
func (f Fields) GetValue(key string) string {
	if v, ok := f.Get(key); ok {
		return v.Value
	}
	return ""
}

// Len returns the number of structured fields.
func (f Fields) Len() int { return len(f) }
