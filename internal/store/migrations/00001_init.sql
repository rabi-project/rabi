-- SPDX-License-Identifier: Apache-2.0
-- +goose Up
CREATE TABLE tangle_info (
    key   text PRIMARY KEY,
    value text NOT NULL
);
INSERT INTO tangle_info (key, value) VALUES ('schema_family', 'v1alpha1');

-- +goose Down
DROP TABLE tangle_info;
