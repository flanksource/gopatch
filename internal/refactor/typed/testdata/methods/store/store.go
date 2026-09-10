package store

// Saver is implemented by both types below, so renaming the method on either
// one has to rename this and the other implementation with it.
type Saver interface {
	Save() error
}

// File saves to disk.
type File struct {
	Path string
}

// Save writes the file.
func (f *File) Save() error { return nil }

// Memory saves nowhere.
type Memory struct{}

// Save discards the data.
func (m Memory) Save() error { return nil }

// SaveAll is what ties the implementations to the interface.
func SaveAll(savers ...Saver) error {
	for _, s := range savers {
		if err := s.Save(); err != nil {
			return err
		}
	}
	return nil
}

func run() error {
	return SaveAll(&File{Path: "a"}, Memory{})
}
