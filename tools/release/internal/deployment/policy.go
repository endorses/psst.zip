package deployment

// FullFlowCheckNames shares the full authenticated-flow policy with read-only
// release evidence consumers. Callers receive their own slice.
func FullFlowCheckNames(restoring bool) []string {
	names := append([]string{}, flowChecks...)
	if restoring {
		names = append(names, restoreChecks...)
	}
	return names
}
