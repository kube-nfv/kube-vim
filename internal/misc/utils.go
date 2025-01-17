package misc

import "github.com/google/uuid"

func IsUUID(uuidStr string) bool {
	_, err := uuid.Parse(uuidStr)
	return err == nil
}
