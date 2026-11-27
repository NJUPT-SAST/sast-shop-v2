# 店铺商品分类实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 subagent-driven-development 并行完成后端和前端任务，遵循 test-driven-development。

**目标：** 实现店铺两级商品分类、分类安全维护、模板分类归属和按分类展示，并只升级本地数据库。

**架构：** Catalog 服务维护店铺分类树，在模板上保存一个可空分类 ID，按服务端校验限制为当前店铺可选的末级分类。前端两端共用 API 和分类规则，分别实现交互。分类相关写入使用 PostgreSQL advisory transaction lock。

**技术栈：** Go、ConnectRPC、Buf、PostgreSQL 16.9 + zhparser、Bun、Next.js、React、TypeScript、Vitest。

**规格：** `docs/store-product-category-design.md`

## 全局约束

- 新增分类表不含 `status`、`created_by_user_id`。
- 普通用户可创建一级/二级分类和删除空分类，不能修改分类名称或排序。
- 有子分类时模板必须选择其中一个二级分类；一级无子分类时可直接关联一级。
- 一级下有未删除商品时，普通用户不能新增二级分类。
- 有商品的分类、含子分类的一级分类不能删除。
- 分类相关写入获取店铺范围的 PostgreSQL 事务 advisory lock；普通模板字段编辑仅使用模板版本控制。
- 店铺浏览默认按排序展示第一个一级，再展示其第一个二级分类；一次只读取当前分类商品。
- PC 店铺商品页隐藏左侧主导航，保留“返回团购工作台”和未提交内容离开确认。
- `.env` 中后端数据库必须为 `127.0.0.1:15432/sast_shop_v2`。不连接、不迁移、不写入远程数据库。
- 保留现有 5432 Windows PostgreSQL 库和后端设计文档的未提交状态。

## 实施接口约定

- `ProductCategory` 字段：`id=1`、`store_id=2`、`optional parent_id=3`、`name=4`、`sort_order=5`。
- `ProductTemplate` 新增 `optional category_id=9`。
- `ProductTemplateService` 新增分类树列表、分类创建和分类删除 RPC。列表按店铺返回 `categories` 和可见未分类商品标志。新建根分类用省略 `parent_id`，新建二级分类传同店一级分类 ID。
- 模板列表新增 `category_filter_mode=6`（UNSPECIFIED=0、UNFILTERED=1、CATEGORY=2、UNCATEGORIZED=3）和 `category_id=7`。未分类筛选单独表达，不以 `category_id=0` 兼作哨兵。指定筛选必须有店铺 ID。
- `CreateProductTemplateRequest` 增加必填 `category_id=7`。更新通过现有 `ProductTemplate.category_id`（optional）和 `FieldMask` 的 `category_id` 路径实现。
- 先修改后端 proto 并同步相同 schema 到前端 proto。Buf 生成文件仅由生成器产生。

## 文件职责

### 后端仓库

- `migrations/010_store_product_categories.sql`：分类表、模板分类列、约束和索引。
- `proto/sast/sastshopv2/catalog/v1/product_category.proto`、`product_template.proto`、`product_template_service.proto`：分类和模板 RPC 契约。
- `internal/service/catalogservice/internal/model/model.go`：分类模型与模板分类字段。
- `internal/service/catalogservice/internal/repository/catalog.go` 及新增 `product_category.go`：树查询、锁内读写、模板分类过滤和统计。
- `internal/service/catalogservice/internal/service/catalog.go` 及新增 `product_category.go`：分类校验、事务锁、模板分类联动。
- `internal/service/catalogservice/internal/handler/v1/`：RPC 注册、成员鉴权和错误映射。
- 现有 Catalog 和 Spot 单测：分类规则、并发冲突、模板字段投影。

### 前端仓库

- `proto/sast/sastshopv2/catalog/v1/`：同步后端契约。
- `packages/api/src/gen/`：Buf 生成 TypeScript RPC 定义。
- `packages/api/src/services/product-categories.ts`：分类 RPC facade、校验与类型映射。
- `packages/api/src/services/product-templates.ts`：分类字段映射、分类筛选和模板写入。
- `packages/api/src/index.ts`：导出分类 API。
- 两端 `product-template-manager.tsx`：模板新增、编辑、分类新建和空分类删除。
- 两端 `cached-product-templates.tsx`、`errand-shop.tsx` 和店铺 route：分类首屏、按当前分类分页、空/未分类状态。
- `apps/desktop/components/desktop-shell.tsx`：店铺商品路由隐藏主导航。
- `apps/desktop/components/errand-shop.tsx`：固定返回入口和未提交草稿离开确认。
- `mock/fauxrpc/stubs/catalog.yaml`：分类 RPC 和分类模板样例。
- 对应 API、两端组件行为测试。

## 审查重点

- 普通模板改名/改价不被分类 advisory lock 阻塞，同一模板的版本冲突仍有效。
- 旧模板未分类可读可展示，编辑必须补分类；已软删除模板不阻挡分类删除。
- 并发新增首个二级分类、一级商品创建和分类删除始终保持归属约束。
- 二级分类必须属于当前一级和店铺，不能创建三级、同级重名或跨店模板分类。
- 分类筛选影响分页和总数，未分类与无筛选不会混淆，不能加载全店全部商品。

---

### 任务 1：后端分类表和 Catalog RPC

**文件：** 后端 `migrations/010_store_product_categories.sql`、Catalog `proto`、model、repository、service、handler 和相关测试。

- [ ] 先为迁移约束、分类列表/创建/删除权限和拒绝条件写测试。
- [ ] 运行目标测试确认失败。
- [ ] 实现迁移、分类模型和 RPC。所有分类写用 advisory xact lock 和同一事务；普通模板字段编辑不申请分类锁。
- [ ] 测试重名、层级、含商品/子类删除拒绝、软删除模板引用释放及并发冲突。
- [ ] 执行 Catalog 服务测试和迁移静态校验。

### 任务 2：模板分类归属和后端分类查询

**文件：** 后端模板 proto、Catalog repository/service/handler、现有权限与查询测试、Spot 模板投影。

- [ ] 为一级/二级末级规则、未分类、店铺隔离、查询分页计数及普通字段编辑锁隔离写测试。
- [ ] 运行测试确认失败。
- [ ] 实现创建/更新/读取字段、FieldMask、按分类过滤和一致计数。创建分类、创建模板、软删除模板和分类归属更新获取同一店铺 advisory lock。字段编辑保持原有行锁和 updated_at 校验。
- [ ] 实现店铺迁移锁定顺序、旧二级分类不得静默跨店复用，以及 Spot 店铺覆盖投影时清理不匹配 category_id。
- [ ] 运行 Catalog、Spot 测试及 Go build。

### 任务 3：前端 API facade 与生成契约

**文件：** 前端 proto、生成 TypeScript、API facade、fauxrpc 分类 stub 和服务层测试。

- [ ] 先新增 RPC transport、参数校验、输出映射和分类过滤测试。
- [ ] 运行测试确认失败。
- [ ] 同步后端 schema 并运行 Buf 生成器。实现 list/create/delete category，模板 category_id 的 create/update/get/list 映射。
- [ ] mock 实现分类树、按分类模板数据和删除业务错误。
- [ ] 运行 API lint、typecheck、相关测试。

### 任务 4：前端模板管理及分类浏览

**文件：** 前端两端模板管理、缓存数据源、店铺 route 与 errand-shop、desktop shell，以及两端行为测试。

- [ ] 为一级后列二级、切换店铺/一级清除旧选择、编辑回显、只加载选择分类、购物车保留、PC 隐藏侧栏及离开确认写行为测试。
- [ ] 运行相关测试确认失败。
- [ ] 接入分类 API 与表单规则。模板有子分类时要求显式选二级；分类创建成功后选中；删除入口只允许删除空叶子或空根。
- [ ] 店铺页默认首个一级及首个二级，首屏只拉取选中末级分类第一页；提供未分类入口。
- [ ] PC 只在店铺商品页隐藏主导航，返回工作台时恢复；实现未提交购物车离开确认。
- [ ] 运行两端行为测试、lint、typecheck、test、build，浏览器检查 PC 和移动布局。

### 任务 5：同步本地开发数据库

**文件：** 仅本地数据库 `127.0.0.1:15432/sast_shop_v2`，不改变任何远程状态。

- [x] 验证 `.env` 仍指向回环地址和本地同步 PostgreSQL 16.9 容器。
- [x] 对 15432 执行 010 增量迁移，使用事务和 ON_ERROR_STOP。
- [x] 验证表、约束、索引、zhparser 和存量数据，迁移与测试仅连接本机。
- [x] 保持本地 5432 Windows 数据库及 `.env` 中其他配置不变。

### 任务 6：最终跨仓审查和回归

- [ ] 审查后端和前端 diff，核对契约生成漂移和 mock。
- [ ] 运行后端相关测试与前端全部 CI 级检查。
- [ ] 检查分类 lock 未扩展到只读浏览和普通不同模板字段编辑。
- [ ] 确认本地迁移只作用于 15432，且远程数据库没有被修改。
