package a11y

import "fmt"

func fmtSscan(s string, px *int) (int, error) { return fmt.Sscanf(s, "%dpx", px) }
