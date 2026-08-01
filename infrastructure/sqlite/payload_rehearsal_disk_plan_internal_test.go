package sqlite

import "testing"

func TestRehearsalDiskPlanNamesBackupScenarios(t *testing.T) {
	tests := []struct {
		name         string
		backupExists bool
		backup       uint64
		total        uint64
	}{
		{name: "missing backup reserves copy", backupExists: false, backup: 100, total: 340},
		{name: "existing backup reserves no copy", backupExists: true, backup: 0, total: 240},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := rehearsalDiskPlan(100, 40, tt.backupExists)
			if plan.BackupBytes != tt.backup || plan.MigrationCloneBytes != 100 || plan.MigrationWALBytes != 40 || plan.ShadowGrowthBytes != 100 || plan.TotalBytes != tt.total {
				t.Fatalf("plan=%+v", plan)
			}
		})
	}
}
