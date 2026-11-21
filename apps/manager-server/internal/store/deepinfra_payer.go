package store

func (s *Store) DeepInfraPayerReference(root string) string {
	return s.officialProtector.DeepInfraPayerReference(root)
}
