-- SPDX-License-Identifier: Apache-2.0
-- +goose Up

-- Probe results (phase1-build-plan.md M12): known-output circuits run on a
-- schedule under the system probe tenant; fidelity feeds target health and
-- |predicted − measured| feeds estimator calibration (the pilot SLO:
-- median abs error ≤ 0.10). Append-only like every measurement table.
CREATE TABLE probe_results (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    at            timestamptz NOT NULL DEFAULT now(),
    target        text NOT NULL,
    job_id        uuid NOT NULL,
    fidelity      double precision NOT NULL,   -- 1 - TVD vs ideal Bell
    predicted_esp double precision,            -- placement prediction, if any
    abs_error     double precision             -- |predicted - fidelity|, if predicted
);
CREATE INDEX probe_results_target_idx ON probe_results (target, at);
REVOKE UPDATE, DELETE, TRUNCATE ON probe_results FROM rabi_app;
