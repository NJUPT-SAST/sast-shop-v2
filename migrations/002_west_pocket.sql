BEGIN;
CREATE SCHEMA IF NOT EXISTS westpocket;

CREATE TABLE westpocket.pocket (
 id bigserial PRIMARY KEY, owner_id bigint NOT NULL, title varchar(100) NOT NULL,
 total_cents integer NOT NULL CHECK(total_cents > 0), status varchar(24) NOT NULL DEFAULT 'draft',
 revision bigint NOT NULL DEFAULT 1, participant_count integer NOT NULL DEFAULT 1,
 owner_share_cents integer NOT NULL DEFAULT 0, receivable_cents integer NOT NULL DEFAULT 0,
 qr_ciphertext bytea, published_at timestamptz, settled_at timestamptz, cancelled_at timestamptz,
 cancel_reason varchar(500) NOT NULL DEFAULT '', created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 CHECK(status IN ('draft','publishing','collecting','settled','cancelling','cancelled'))
);
CREATE INDEX ON westpocket.pocket(owner_id, id DESC);
CREATE INDEX ON westpocket.pocket(status, updated_at);
CREATE TABLE westpocket.pocket_member (
 id bigserial PRIMARY KEY, pocket_id bigint NOT NULL REFERENCES westpocket.pocket(id),
 user_id bigint NOT NULL, selection_source varchar(16) NOT NULL DEFAULT 'search',
 face_match_id bigint, share_cents integer NOT NULL DEFAULT 0 CHECK(share_cents >= 0),
 payment_bill_id bigint UNIQUE, bill_status varchar(24) NOT NULL DEFAULT '',
 album_access varchar(16) NOT NULL DEFAULT 'pending', last_reminded_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(pocket_id,user_id)
);
CREATE INDEX ON westpocket.pocket_member(user_id,pocket_id DESC);
CREATE TABLE westpocket.upload (
 id bigserial PRIMARY KEY, owner_id bigint NOT NULL, pocket_id bigint REFERENCES westpocket.pocket(id),
 purpose varchar(24) NOT NULL CHECK(purpose IN ('face_sample','group_photo')),
 status varchar(16) NOT NULL DEFAULT 'uploading', object_key text NOT NULL UNIQUE, sha256 char(64) NOT NULL, byte_size bigint NOT NULL,
 width integer NOT NULL, height integer NOT NULL, consent_version text NOT NULL,
 request_id uuid NOT NULL, bound boolean NOT NULL DEFAULT false,
 expires_at timestamptz NOT NULL, deleted_at timestamptz, created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(owner_id,request_id)
);
CREATE INDEX ON westpocket.upload(expires_at) WHERE deleted_at IS NULL;
CREATE TABLE westpocket.pocket_photo (
 id bigserial PRIMARY KEY, pocket_id bigint NOT NULL REFERENCES westpocket.pocket(id),
 upload_id bigint NOT NULL UNIQUE REFERENCES westpocket.upload(id), uploader_id bigint NOT NULL,
 object_key text NOT NULL, sha256 char(64) NOT NULL, width integer NOT NULL, height integer NOT NULL,
 status varchar(24) NOT NULL DEFAULT 'uploaded', latest_job_id bigint,
 detected_face_count integer NOT NULL DEFAULT 0, retention_mode varchar(24) NOT NULL DEFAULT 'temporary',
 retention_until timestamptz NOT NULL, authorization_version text NOT NULL,
 error_code text NOT NULL DEFAULT '', deleted_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX pocket_photo_live_hash ON westpocket.pocket_photo(pocket_id,sha256) WHERE deleted_at IS NULL;
CREATE TABLE westpocket.face_profile (
 id bigserial PRIMARY KEY, user_id bigint NOT NULL UNIQUE, person_id text NOT NULL DEFAULT '',
 status varchar(24) NOT NULL DEFAULT 'pending', revision bigint NOT NULL DEFAULT 1,
 enrollment_version bigint NOT NULL DEFAULT 1, consent_version text NOT NULL,
 consented_at timestamptz NOT NULL, consent_expires_at timestamptz NOT NULL,
 revoked_at timestamptz, provider_deleted_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX ON westpocket.face_profile(person_id) WHERE person_id <> '';
CREATE TABLE westpocket.face_sample (
 id bigserial PRIMARY KEY, profile_id bigint NOT NULL REFERENCES westpocket.face_profile(id),
 upload_id bigint NOT NULL UNIQUE REFERENCES westpocket.upload(id), enrollment_version bigint NOT NULL,
 person_id text NOT NULL, status varchar(24) NOT NULL DEFAULT 'pending',
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE westpocket.async_job (
 id bigserial PRIMARY KEY, kind varchar(32) NOT NULL, owner_id bigint NOT NULL,
 pocket_id bigint REFERENCES westpocket.pocket(id), profile_id bigint REFERENCES westpocket.face_profile(id),
 input_version bigint NOT NULL DEFAULT 0, payload jsonb NOT NULL DEFAULT '{}',
 dedupe_key text NOT NULL UNIQUE, status varchar(24) NOT NULL DEFAULT 'queued',
 attempt_count integer NOT NULL DEFAULT 0, next_attempt_at timestamptz NOT NULL DEFAULT now(),
 lease_owner text NOT NULL DEFAULT '', lease_expires_at timestamptz,
 error_code text NOT NULL DEFAULT '', progress jsonb NOT NULL DEFAULT '{}',
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON westpocket.async_job(status,next_attempt_at);
CREATE TABLE westpocket.face_match (
 id bigserial PRIMARY KEY, photo_id bigint NOT NULL REFERENCES westpocket.pocket_photo(id),
 job_id bigint NOT NULL REFERENCES westpocket.async_job(id), face_index integer NOT NULL,
 bbox_x integer NOT NULL, bbox_y integer NOT NULL, bbox_width integer NOT NULL, bbox_height integer NOT NULL,
 suggested_user_id bigint, confirmed_user_id bigint, candidates jsonb NOT NULL DEFAULT '[]',
 score double precision NOT NULL DEFAULT 0, match_status varchar(24) NOT NULL,
 resolution varchar(16) NOT NULL DEFAULT 'pending', expires_at timestamptz NOT NULL,
 UNIQUE(job_id,photo_id,face_index)
);
ALTER TABLE westpocket.pocket_member ADD FOREIGN KEY(face_match_id) REFERENCES westpocket.face_match(id) ON DELETE SET NULL;
CREATE TABLE westpocket.notification_outbox (
 id bigserial PRIMARY KEY, pocket_id bigint NOT NULL REFERENCES westpocket.pocket(id),
 recipient_user_id bigint NOT NULL, kind varchar(32) NOT NULL,
 sequence integer NOT NULL DEFAULT 0, status varchar(24) NOT NULL DEFAULT 'pending',
 message_uuid uuid NOT NULL UNIQUE, feishu_message_id text NOT NULL DEFAULT '',
 attempt_count integer NOT NULL DEFAULT 0, next_attempt_at timestamptz NOT NULL DEFAULT now(),
 lease_expires_at timestamptz, error_code text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(pocket_id,recipient_user_id,kind,sequence)
);
CREATE INDEX ON westpocket.notification_outbox(status,next_attempt_at);
CREATE TABLE westpocket.consent_event (
 id bigserial PRIMARY KEY, user_id bigint NOT NULL, purpose varchar(32) NOT NULL,
 action varchar(16) NOT NULL, policy_version text NOT NULL,
 profile_id bigint, pocket_id bigint, photo_id bigint, occurred_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE westpocket.request_dedup (
 actor_id bigint NOT NULL, rpc_method text NOT NULL, request_id uuid NOT NULL,
 request_hash char(64) NOT NULL, resource_id bigint NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(actor_id,rpc_method,request_id)
);
COMMIT;
