package helpers

// Strim is a local helper function to Strim spaces.
// func Strim(str string) string {
// 	return strings.Trim(str, " ")
// }

// Bracketed returns the string bounded with "[]".
func Bracketed(str string) string {
	return BoundStr(str, "[", "]")
}

// BoundStr bounds str by left and right.
func BoundStr(str, left, right string) string {
	return left + str + right
}

// Map2List returns
// func Map2List[T any, I comparable](m map[I]T) []T {
// 	if m == nil {
// 		return []T{}
// 	}

// 	res := make([]T, len(m))

// 	i := 0
// 	for _, v := range m {
// 		res[i] = v

// 		i++
// 	}

// 	return res
// }

// MapKeys returns a slice of map m key.
func MapKeys[T any, I comparable](m map[I]T) []I {
	if len(m) == 0 {
		return []I{}
	}

	res := make([]I, 0, len(m))
	for k := range m {
		res = append(res, k)
	}

	return res
}

// Indes is helper function which returns index of item in items slice
// or -1 if slice doesn't found the item.
func Index[T comparable](item T, slice []T) int {
	if slice == nil {
		return -1
	}

	for i, it := range slice {
		if it == item {
			return i
		}
	}

	return -1
}
