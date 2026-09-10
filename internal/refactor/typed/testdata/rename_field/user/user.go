package user

// User is a person.
type User struct {
	Name string `json:"name"`
	Age  int
}

// Other has a field with the same name, which must not be touched.
type Other struct {
	Name string
}

func (u *User) Greet() string {
	return "hello " + u.Name
}

func NewUser(name string) *User {
	return &User{Name: name}
}

func unrelated(o Other) string {
	return o.Name
}
