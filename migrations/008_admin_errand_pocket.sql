BEGIN;
ALTER TABLE errand.errand_demand DROP CONSTRAINT errand_demand_deadline_check;
ALTER TABLE errand.errand_demand ADD CONSTRAINT errand_demand_deadline_after_creation CHECK(deadline>created_at);
ALTER TABLE westpocket.async_job ADD COLUMN lifecycle_round bigint NOT NULL DEFAULT 1 CHECK(lifecycle_round>0);
ALTER TABLE westpocket.notification_outbox ADD COLUMN lifecycle_round bigint NOT NULL DEFAULT 1 CHECK(lifecycle_round>0);
DO $$ DECLARE old_constraint record; BEGIN
 FOR old_constraint IN SELECT conname FROM pg_constraint
  WHERE conrelid='westpocket.notification_outbox'::regclass AND contype='u'
    AND pg_get_constraintdef(oid)='UNIQUE (pocket_id, recipient_user_id, kind, sequence)'
 LOOP EXECUTE format('ALTER TABLE westpocket.notification_outbox DROP CONSTRAINT %I',old_constraint.conname); END LOOP;
END $$;
CREATE UNIQUE INDEX notification_outbox_round_unique ON westpocket.notification_outbox(pocket_id,recipient_user_id,kind,sequence,lifecycle_round);

CREATE FUNCTION "user".guard_admin_parent() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE operation text; parent_id bigint;
BEGIN
 IF TG_TABLE_SCHEMA='westpocket' THEN
  IF TG_OP='DELETE' THEN parent_id:=OLD.pocket_id; ELSE parent_id:=NEW.pocket_id; END IF;
  IF parent_id IS NULL THEN IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF; END IF;
  SELECT admin_operation_id INTO operation FROM westpocket.pocket WHERE id=parent_id FOR SHARE;
 ELSIF TG_TABLE_NAME='errand_demand_item' THEN
  IF TG_OP='DELETE' THEN parent_id:=OLD.errand_demand_id; ELSE parent_id:=NEW.errand_demand_id; END IF;
  SELECT admin_operation_id INTO operation FROM errand.errand_demand WHERE id=parent_id FOR SHARE;
 ELSE
  IF TG_OP='DELETE' THEN parent_id:=OLD.task_id; ELSE parent_id:=NEW.task_id; END IF;
  SELECT admin_operation_id INTO operation FROM errand.errand_task WHERE id=parent_id FOR SHARE;
 END IF;
 IF operation<>'' AND operation IS DISTINCT FROM current_setting('sast.admin_operation_id',true)
 THEN RAISE EXCEPTION 'parent resource is being corrected' USING ERRCODE='55000'; END IF;
 IF TG_TABLE_SCHEMA='errand' AND TG_TABLE_NAME='errand_task_assignment' THEN
  IF TG_OP='DELETE' THEN parent_id:=OLD.demand_item_id; ELSE parent_id:=NEW.demand_item_id; END IF;
  SELECT d.admin_operation_id INTO operation FROM errand.errand_demand d
   JOIN errand.errand_demand_item i ON i.errand_demand_id=d.id WHERE i.id=parent_id FOR SHARE OF d;
  IF operation<>'' AND operation IS DISTINCT FROM current_setting('sast.admin_operation_id',true)
   THEN RAISE EXCEPTION 'assigned demand is being corrected' USING ERRCODE='55000'; END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END $$;
CREATE TRIGGER guard_admin_parent BEFORE INSERT OR UPDATE OR DELETE ON errand.errand_task_item
 FOR EACH ROW EXECUTE FUNCTION "user".guard_admin_parent();
CREATE TRIGGER guard_admin_parent BEFORE INSERT OR UPDATE OR DELETE ON errand.errand_task_assignment
 FOR EACH ROW EXECUTE FUNCTION "user".guard_admin_parent();
CREATE TRIGGER guard_admin_parent BEFORE INSERT OR UPDATE OR DELETE ON errand.errand_demand_item
 FOR EACH ROW EXECUTE FUNCTION "user".guard_admin_parent();
CREATE TRIGGER guard_admin_parent BEFORE INSERT OR UPDATE OR DELETE ON westpocket.pocket_member
 FOR EACH ROW EXECUTE FUNCTION "user".guard_admin_parent();
COMMIT;
