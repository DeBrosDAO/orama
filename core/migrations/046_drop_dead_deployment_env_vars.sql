-- Migration 046: remove the deployment_env_vars table.
--
-- 007_deployments.sql created it with the comment "separate for security" and
-- a column commented "Plaintext JSON (not encrypted)". Nothing has ever written
-- to it or read from it: a deployment's environment has always lived in
-- deployments.environment. The table's only effect was to suggest that
-- environment variables were held somewhere deliberate and safe while they were
-- in fact stored in the clear in another table.
--
-- deployments.environment is encrypted now, so the table is not a plan waiting
-- to be finished; it is a claim that was never true. It is always empty, so
-- dropping it takes nothing with it.
BEGIN;

DROP TABLE IF EXISTS deployment_env_vars;

COMMIT;
