package change

func AnalyzeDataCapacityChange(oldCapacity, newCapacity string) CapacityChange {
	oldParsed := SilentParseResourceQuantity(oldCapacity)
	newParsed := SilentParseResourceQuantity(newCapacity)

	if oldCapacity == newCapacity {
		return CapacityNoChange
	}

	cmp := newParsed.Cmp(oldParsed)
	if cmp == 0 {
		// Same numeric value, only syntactic change (e.g. 1024Mi -> 1Gi)
		return CapacitySyntacticChange
	}

	if cmp > 0 {
		return CapacityUpsize
	}

	return CapacityDownsize
}

type CapacityChange int

const (
	CapacityNoChange CapacityChange = iota
	CapacitySyntacticChange
	CapacityUpsize
	CapacityDownsize
)
