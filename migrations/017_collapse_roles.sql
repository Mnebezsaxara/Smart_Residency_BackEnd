-- Collapse the role model down to the three roles described in the thesis:
--   admin    — administrators
--   staff    — all employees, INCLUDING security guards (specialty='security')
--   resident — all tenants, INCLUDING former owners
--
-- The extra 'guard' and 'owner' roles crept in during development. This
-- migration folds them back in. All statements are idempotent (UPDATE by role
-- value), so re-running on every startup is safe.

-- Former guards become security staff.
UPDATE profiles
   SET role = 'staff',
       specialty = 'security'
 WHERE role = 'guard';

-- Former owners (and any stray 'tenant') become plain residents.
UPDATE profiles
   SET role = 'resident'
 WHERE role IN ('owner', 'tenant');

-- Pending verification requests must not re-introduce the removed roles when
-- an admin approves them. Self-service registration can only ever ask for
-- 'resident'; security/staff are created via POST /admin/staff.
UPDATE verification_requests
   SET requested_role = 'resident'
 WHERE requested_role IN ('owner', 'tenant', 'guard');
