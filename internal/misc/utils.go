package misc

import "github.com/google/uuid"

func IsUUID(uuidStr string) (err error) {
    _, err = uuid.Parse(uuidStr)
    return
}
