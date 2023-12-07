package utils

// GenericFlag encapsulates common attributes and an interface for value
type Flag struct {
	Name         string
	Abbreviation string
	Value        interface{}
	Usage        string
}

// Implementing the Flag interface
func (f *Flag) GetName() string         { return f.Name }
func (f *Flag) GetAbbreviation() string { return f.Abbreviation }
func (f *Flag) GetUsage() string        { return f.Usage }
func (f *Flag) GetValue() interface{}   { return f.Value }
