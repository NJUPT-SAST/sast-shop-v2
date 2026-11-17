BEGIN;
-- 复制审计表
-- INCLUDING ALL 等于把母版表的默认值、约束、索引、IDENTITY、存储参数、注释全部复制过来（唯一不复制的是外键和触发器，这点后面会讲）。
-- 四个 schema 各复制两份（日志 + 去重），共 8 张表。
CREATE TABLE payment.admin_action_log (LIKE "user".admin_action_log INCLUDING ALL);
CREATE TABLE payment.admin_request_dedup (LIKE "user".admin_request_dedup INCLUDING ALL);
CREATE TABLE errand.admin_action_log (LIKE "user".admin_action_log INCLUDING ALL);
CREATE TABLE errand.admin_request_dedup (LIKE "user".admin_request_dedup INCLUDING ALL);
CREATE TABLE westpocket.admin_action_log (LIKE "user".admin_action_log INCLUDING ALL);
CREATE TABLE westpocket.admin_request_dedup (LIKE "user".admin_request_dedup INCLUDING ALL);
-- 四个 schema 各一份，结构完全相同
-- 一张手写的任务队列，用来跑"管理员发起的、可能失败、需要重试、必须留痕"的高危操作（重建账单、撤销确认、退钱）
CREATE TABLE spot.admin_correction (
    -- 作业 id，用 text 不用自增。因为它在作业创建前就要定下来，还要被前端、日志、outbox 多方引用，必须能跨库/跨进程传递
 id text PRIMARY KEY,
 -- 谁发起的
 actor_id bigint NOT NULL,
 -- 谁执行/批准的。与 actor 分离是为了支持双人复核：发起的人不一定有权限执行
 executor_id bigint NOT NULL,
 -- 资源类型，用数字编码（如 1=现货订单、2=跑腿任务、3=支付账单、4=钱包）。紧凑，队列里传输省空间，代价是可读性差、要维护字典
 resource integer NOT NULL,
 -- 具体哪个资源，与 resource 组成多态引用
 resource_id bigint NOT NULL,
 -- 做什么纠错
 action text NOT NULL,
 -- preview	算好了影响面，等人点确认
 -- executing	已占租约，正在跑
 -- retrying	可恢复的失败，等 next_attempt_at 再来
 -- completed	成功终态
 -- needs_attention	对不上账、需要人来看
 status text NOT NULL CHECK (status IN ('preview','executing','retrying','completed','needs_attention','failed')),
 -- 预览内容的指纹。用户点"确认执行"时必须回传，服务端比对。防止"预览时看到的是一回事，执行的是另一回事"（前端篡改或数据已变）
 digest text NOT NULL,
 -- 给人看的影响范围，用来渲染确认页
 plan jsonb NOT NULL,
 -- 给机器跑的步骤清单及进度，是断点续跑的依据
 steps jsonb NOT NULL DEFAULT '[]',
 -- 客户端请求 id，做幂等
 request_id text NOT NULL DEFAULT '',
 -- 资源预占标记：这个资源已被本次纠错"预定"，别的作业别碰。是跨作业的互斥位
 reserved boolean NOT NULL DEFAULT false,
 -- 执行权令牌。worker 拿到租约后写入随机串，续租/提交都要校验，防止"我以为我还持有租约，其实早被别人接手了"导致两个进程同时改数据
 lease_token text NOT NULL DEFAULT '',
 -- 租约到期时间。worker 崩了租约自然过期，别人可接手，不需要人工清理僵尸任务
 lease_until timestamptz,
 -- 下次可尝试时间。退避重试就是把它往后推
 next_attempt_at timestamptz NOT NULL DEFAULT now(),
 -- 已尝试次数。用来算指数退避、判断是否升级为人工介入
 attempts integer NOT NULL DEFAULT 0,
 error text NOT NULL DEFAULT '',
 expires_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON spot.admin_correction (status,next_attempt_at);
CREATE INDEX ON spot.admin_correction (resource,resource_id,created_at DESC);
CREATE UNIQUE INDEX ON spot.admin_correction (actor_id,request_id) WHERE request_id<>'';
CREATE TABLE errand.admin_correction (
 id text PRIMARY KEY,
 actor_id bigint NOT NULL,
 executor_id bigint NOT NULL,
 resource integer NOT NULL,
  resource_id bigint NOT NULL,
  action text NOT NULL,
 status text NOT NULL CHECK (status IN ('preview','executing','retrying','completed','needs_attention','failed')),
 digest text NOT NULL,
 plan jsonb NOT NULL,
 steps jsonb NOT NULL DEFAULT '[]',
 request_id text NOT NULL DEFAULT '',
 reserved boolean NOT NULL DEFAULT false,
 lease_token text NOT NULL DEFAULT '',
 lease_until timestamptz,
 next_attempt_at timestamptz NOT NULL DEFAULT now(),
 attempts integer NOT NULL DEFAULT 0,
 error text NOT NULL DEFAULT '',
 expires_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON errand.admin_correction (status,next_attempt_at);
CREATE INDEX ON errand.admin_correction (resource,resource_id,created_at DESC);
CREATE UNIQUE INDEX ON errand.admin_correction (actor_id,request_id) WHERE request_id<>'';
CREATE TABLE payment.admin_correction (
 id text PRIMARY KEY,
 actor_id bigint NOT NULL,
 executor_id bigint NOT NULL,
 resource integer NOT NULL,
 resource_id bigint NOT NULL,
 action text NOT NULL,
 status text NOT NULL CHECK (status IN ('preview','executing','retrying','completed','needs_attention','failed')),
 digest text NOT NULL,
 plan jsonb NOT NULL,
 steps jsonb NOT NULL DEFAULT '[]',
 request_id text NOT NULL DEFAULT '',
 reserved boolean NOT NULL DEFAULT false,
 lease_token text NOT NULL DEFAULT '',
 lease_until timestamptz,
 next_attempt_at timestamptz NOT NULL DEFAULT now(),
 attempts integer NOT NULL DEFAULT 0,
 error text NOT NULL DEFAULT '',
 expires_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON payment.admin_correction (status,next_attempt_at);
CREATE INDEX ON payment.admin_correction (resource,resource_id,created_at DESC);
CREATE UNIQUE INDEX ON payment.admin_correction (actor_id,request_id) WHERE request_id<>'';
CREATE TABLE westpocket.admin_correction (
 id text PRIMARY KEY, actor_id bigint NOT NULL, executor_id bigint NOT NULL,
 resource integer NOT NULL, resource_id bigint NOT NULL, action text NOT NULL,
 status text NOT NULL CHECK (status IN ('preview','executing','retrying','completed','needs_attention','failed')),
 digest text NOT NULL, plan jsonb NOT NULL, steps jsonb NOT NULL DEFAULT '[]',
 request_id text NOT NULL DEFAULT '', reserved boolean NOT NULL DEFAULT false,
 lease_token text NOT NULL DEFAULT '', lease_until timestamptz,
 next_attempt_at timestamptz NOT NULL DEFAULT now(), attempts integer NOT NULL DEFAULT 0,
 error text NOT NULL DEFAULT '', expires_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON westpocket.admin_correction (status,next_attempt_at);
CREATE INDEX ON westpocket.admin_correction (resource,resource_id,created_at DESC);
CREATE UNIQUE INDEX ON westpocket.admin_correction (actor_id,request_id) WHERE request_id<>'';
ALTER TABLE spot.spot_order
 ADD COLUMN lifecycle_round bigint NOT NULL DEFAULT 1 CHECK (lifecycle_round>0),
 ADD COLUMN admin_operation_id text NOT NULL DEFAULT '';
ALTER TABLE errand.errand_task
 ADD COLUMN lifecycle_round bigint NOT NULL DEFAULT 1 CHECK (lifecycle_round>0),
 ADD COLUMN admin_operation_id text NOT NULL DEFAULT '';
ALTER TABLE errand.errand_demand
 ADD COLUMN lifecycle_round bigint NOT NULL DEFAULT 1 CHECK (lifecycle_round>0),
 ADD COLUMN admin_operation_id text NOT NULL DEFAULT '';
ALTER TABLE payment.payment_bill
 ADD COLUMN lifecycle_round bigint NOT NULL DEFAULT 1 CHECK (lifecycle_round>0),
 ADD COLUMN admin_operation_id text NOT NULL DEFAULT '';
ALTER TABLE westpocket.pocket
 ADD COLUMN lifecycle_round bigint NOT NULL DEFAULT 1 CHECK (lifecycle_round>0),
 ADD COLUMN admin_operation_id text NOT NULL DEFAULT '';
ALTER TABLE payment.payment_bill ADD COLUMN superseded_at timestamptz;
ALTER TABLE payment.payment_bill ADD COLUMN funding_bill_id bigint REFERENCES payment.payment_bill(id);
DROP INDEX payment.uq_payment_bill_source;
CREATE UNIQUE INDEX uq_payment_bill_source_round ON payment.payment_bill(source_type,source_id,payer_id,lifecycle_round)
 WHERE source_type IS NOT NULL AND source_id IS NOT NULL;
CREATE UNIQUE INDEX uq_payment_bill_current_source ON payment.payment_bill(source_type,source_id,payer_id)
 WHERE source_type IS NOT NULL AND source_id IS NOT NULL AND superseded_at IS NULL;
CREATE TABLE payment.admin_refund (
 id bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
 bill_id bigint NOT NULL UNIQUE REFERENCES payment.payment_bill(id),
 amount_cents integer NOT NULL CHECK(amount_cents>0),
 actor_id bigint NOT NULL, operation_id text NOT NULL,
 reason text NOT NULL, evidence text NOT NULL,
 verified_at timestamptz NOT NULL DEFAULT now(),
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE payment.admin_source_operation (
 operation_id text PRIMARY KEY, actor_id bigint NOT NULL,
 input_hash text NOT NULL, response jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE payment.admin_confirmation_reversal (
 id bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
 bill_id bigint NOT NULL UNIQUE REFERENCES payment.payment_bill(id),
 actor_id bigint NOT NULL, operation_id text NOT NULL,
 reason text NOT NULL, evidence text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE payment.business_outbox (
 id bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
 event_key text NOT NULL UNIQUE, bill_id bigint NOT NULL, lifecycle_round bigint NOT NULL,
 status text NOT NULL DEFAULT 'pending', attempts integer NOT NULL DEFAULT 0,
 next_attempt_at timestamptz NOT NULL DEFAULT now(), last_error text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE spot.admin_round_history (
 resource text NOT NULL, resource_id bigint NOT NULL, lifecycle_round bigint NOT NULL,
 operation_id text NOT NULL, snapshot jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(resource,resource_id,lifecycle_round)
);
CREATE TABLE errand.admin_round_history (
 resource text NOT NULL, resource_id bigint NOT NULL, lifecycle_round bigint NOT NULL,
 operation_id text NOT NULL, snapshot jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(resource,resource_id,lifecycle_round)
);
CREATE TABLE westpocket.admin_round_history (
 resource text NOT NULL, resource_id bigint NOT NULL, lifecycle_round bigint NOT NULL,
 operation_id text NOT NULL, snapshot jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(resource,resource_id,lifecycle_round)
);
CREATE OR REPLACE FUNCTION "user".guard_admin_correction() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.admin_operation_id<>'' AND OLD.admin_operation_id IS DISTINCT FROM current_setting('sast.admin_operation_id',true)
 THEN RAISE EXCEPTION 'resource is being corrected' USING ERRCODE='55000'; END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER guard_admin_correction BEFORE UPDATE OR DELETE ON spot.spot_order
 FOR EACH ROW EXECUTE FUNCTION "user".guard_admin_correction();
CREATE TRIGGER guard_admin_correction BEFORE UPDATE OR DELETE ON errand.errand_task
 FOR EACH ROW EXECUTE FUNCTION "user".guard_admin_correction();
CREATE TRIGGER guard_admin_correction BEFORE UPDATE OR DELETE ON errand.errand_demand
 FOR EACH ROW EXECUTE FUNCTION "user".guard_admin_correction();
CREATE TRIGGER guard_admin_correction BEFORE UPDATE OR DELETE ON payment.payment_bill
 FOR EACH ROW EXECUTE FUNCTION "user".guard_admin_correction();
CREATE TRIGGER guard_admin_correction BEFORE UPDATE OR DELETE ON westpocket.pocket
 FOR EACH ROW EXECUTE FUNCTION "user".guard_admin_correction();

CREATE OR REPLACE FUNCTION payment.enqueue_bill_confirmation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.status='completed' AND OLD.status IS DISTINCT FROM NEW.status AND NEW.source_type='errand_task' AND NEW.superseded_at IS NULL THEN
  INSERT INTO payment.business_outbox(event_key,bill_id,lifecycle_round)
  VALUES('confirmed:'||NEW.id||':'||NEW.lifecycle_round,NEW.id,NEW.lifecycle_round)
  ON CONFLICT(event_key) DO NOTHING;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER enqueue_bill_confirmation AFTER UPDATE ON payment.payment_bill
FOR EACH ROW EXECUTE FUNCTION payment.enqueue_bill_confirmation();

CREATE OR REPLACE FUNCTION payment.guard_source_round() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE source_table text; source_round bigint; operation text; permitted text;
BEGIN
 source_table:=CASE NEW.source_type WHEN 'spot_order' THEN 'spot.spot_order'
  WHEN 'errand_task' THEN 'errand.errand_task' WHEN 'west_pocket' THEN 'westpocket.pocket' END;
 IF source_table IS NULL OR NEW.source_id IS NULL THEN RETURN NEW; END IF;
 EXECUTE format('SELECT lifecycle_round,admin_operation_id FROM %s WHERE id=$1',source_table)
 INTO source_round,operation USING NEW.source_id;
 permitted:=current_setting('sast.admin_operation_id',true);
 IF operation<>'' AND operation IS DISTINCT FROM permitted THEN
  RAISE EXCEPTION 'source is being corrected' USING ERRCODE='55000';
 END IF;
 IF operation IS NOT NULL AND operation<>'' AND operation=permitted THEN RETURN NEW; END IF;
 IF source_round IS NOT NULL AND NEW.lifecycle_round<>source_round THEN
  RAISE EXCEPTION 'stale payment lifecycle round' USING ERRCODE='55000';
 END IF;
 IF source_round IS NULL AND NEW.lifecycle_round<>1 THEN
  RAISE EXCEPTION 'missing source lifecycle round' USING ERRCODE='55000';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER guard_source_round BEFORE INSERT OR UPDATE ON payment.payment_bill
FOR EACH ROW EXECUTE FUNCTION payment.guard_source_round();

COMMIT;
