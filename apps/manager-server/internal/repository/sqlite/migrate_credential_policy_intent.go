package sqlite

import "database/sql"

func ensureCredentialPolicyAlwaysOnIntent(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.Exec(`UPDATE credential_policies SET generation=generation+1,document=json_set(document,'$.enabled',json('true'),'$.generation',generation+1) WHERE json_valid(document) AND json_type(document)='object' AND generation>0 AND generation<9007199254740991 AND json_type(document,'$.enabled')='false' AND (json_type(document,'$.generation') IS NULL OR (json_type(document,'$.generation')='integer' AND json_extract(document,'$.generation')=generation))`)
	if err != nil {
		return err
	}
	return tx.Commit()
}
