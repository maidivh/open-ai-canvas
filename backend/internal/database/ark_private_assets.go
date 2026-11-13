package database

import (
	"fmt"
	"slices"
	"strings"

	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

const arkBindingIdentityIndex = "idx_ark_private_asset_binding_resource_project"

// Existing databases do not rerun the baseline AutoMigrate when model indexes change.
func migrateArkPrivateAssetBindingIdentity(tx *gorm.DB) error {
	binding := &model.ArkPrivateAssetBinding{}
	if !tx.Migrator().HasTable(binding) {
		return tx.AutoMigrate(binding)
	}
	columns := []string{"resource_id", "project_name"}
	if tx.Migrator().HasColumn(binding, "Site") {
		columns = append(columns, "site")
	}
	identity := strings.Join(columns, ", ")
	slices.Sort(columns)
	const index = arkBindingIdentityIndex
	if tx.Migrator().HasIndex(binding, index) {
		indexes, err := tx.Migrator().GetIndexes(binding)
		if err != nil {
			return fmt.Errorf("读取方舟可信素材绑定索引：%w", err)
		}
		for _, existing := range indexes {
			if existing.Name() != index {
				continue
			}
			unique, known := existing.Unique()
			actual := slices.Clone(existing.Columns())
			slices.Sort(actual)
			if !known || !unique || !slices.Equal(actual, columns) {
				break
			}
			// GetIndexes omits predicates and PostgreSQL validity/immediacy; those
			// also determine whether an index can arbitrate this ON CONFLICT.
			var usable bool
			var query string
			switch tx.Dialector.Name() {
			case "sqlite":
				query = `SELECT "partial" = 0 FROM pragma_index_list('ark_private_asset_bindings') WHERE name = ?`
			case "postgres":
				query = `SELECT i.indisvalid AND i.indisready AND i.indimmediate
					AND i.indpred IS NULL AND i.indexprs IS NULL AND i.indnkeyatts = ?
					FROM pg_index i JOIN pg_class idx ON idx.oid = i.indexrelid
					JOIN pg_namespace ns ON ns.oid = idx.relnamespace
					WHERE idx.relname = ? AND ns.nspname = current_schema()`
			default:
				return fmt.Errorf("不支持校验方舟可信素材绑定索引的数据库：%s", tx.Dialector.Name())
			}
			args := []any{index}
			if tx.Dialector.Name() == "postgres" {
				args = []any{len(columns), index}
			}
			if err := tx.Raw(query, args...).Scan(&usable).Error; err != nil {
				return fmt.Errorf("检查方舟可信素材绑定索引仲裁条件：%w", err)
			}
			if usable {
				return nil
			}
			break
		}
		return fmt.Errorf("索引 %s 定义不匹配：必须是完整、有效、可立即仲裁的 (%s) 复合唯一索引；请核对并修复索引后重试迁移，未修改已有索引或绑定", index, identity)
	}
	var duplicates int64
	if err := tx.Raw(`SELECT COUNT(*) FROM (
		SELECT ` + identity + ` FROM ark_private_asset_bindings
		GROUP BY ` + identity + ` HAVING COUNT(*) > 1
	) AS duplicate_bindings`).Scan(&duplicates).Error; err != nil {
		return fmt.Errorf("检查方舟可信素材重复绑定：%w", err)
	}
	if duplicates > 0 {
		return fmt.Errorf("ark_private_asset_bindings 存在 %d 组重复的 (%s)，请先核对方舟素材 ID 与审核状态并处理重复绑定后重试迁移；未删除任何绑定", duplicates, identity)
	}
	return tx.Exec("CREATE UNIQUE INDEX " + index + " ON ark_private_asset_bindings (" + identity + ")").Error
}

// 已有绑定默认归属火山引擎，随后以供应商隔离素材 ID。
func migrateArkAssetProvider(tx *gorm.DB) error {
	binding := &model.ArkPrivateAssetBinding{}
	if !tx.Migrator().HasTable(binding) || tx.Migrator().HasColumn(binding, "Site") {
		return migrateArkPrivateAssetBindingIdentity(tx)
	}
	if err := migrateArkPrivateAssetBindingIdentity(tx); err != nil {
		return err
	}
	if err := tx.Migrator().AddColumn(binding, "Site"); err != nil {
		return fmt.Errorf("增加素材绑定站点列：%w", err)
	}
	if err := tx.Exec("UPDATE ark_private_asset_bindings SET site = 'volcengine' WHERE site IS NULL OR site = ''").Error; err != nil {
		return fmt.Errorf("回填素材绑定站点：%w", err)
	}
	if err := tx.Exec("DROP INDEX " + arkBindingIdentityIndex).Error; err != nil {
		return fmt.Errorf("删除旧素材绑定索引：%w", err)
	}
	return migrateArkPrivateAssetBindingIdentity(tx)
}
