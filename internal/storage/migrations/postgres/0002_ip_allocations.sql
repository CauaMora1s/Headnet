-- Addresses handed out from the overlay pools.
--
-- The primary key on `address` is the real guarantee that no address is ever
-- issued twice. The allocator's scan for a free address is only an
-- optimisation for choosing a candidate; correctness under concurrent
-- enrolment rests on this constraint, not on application logic.
--
-- There is deliberately no foreign key to `devices`: that table does not exist
-- yet, and SQLite cannot add a constraint afterwards without rebuilding the
-- table. Integrity comes instead from allocating and releasing inside the
-- caller's transaction, so a device and its address are created or discarded
-- together.
CREATE TABLE ip_allocations (
    address      TEXT        NOT NULL PRIMARY KEY,
    family       INTEGER     NOT NULL,
    owner_id     TEXT        NOT NULL,
    allocated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_ip_allocations_owner ON ip_allocations (owner_id);
CREATE INDEX idx_ip_allocations_family ON ip_allocations (family);
