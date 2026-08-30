/**
 * The administrative areas the web UI will eventually cover.
 *
 * They are listed with the roadmap phase that will deliver each one, and are
 * rendered as visibly unavailable. Showing an empty "Devices" table would read
 * as "you have no devices" rather than "this has not been built", which is the
 * distinction this whole build is careful about.
 *
 * The same honesty applies in the other direction: once an API exists, saying
 * it does not is just as wrong. `status` distinguishes an area that has not
 * been built at all from one whose API works and is only missing its screens.
 */
export type AreaStatus = 'planned' | 'api-only';

export interface PlannedArea {
  name: string;
  phase: string;
  summary: string;
  status: AreaStatus;
}

export const PLANNED_AREAS: readonly PlannedArea[] = [
  {
    name: 'Devices',
    phase: 'Phase 1',
    summary: 'Enrol, inspect and revoke the machines on this network.',
    status: 'planned',
  },
  {
    name: 'Users',
    phase: 'Phase 1',
    summary: 'Accounts exist and can be created; the management screens do not.',
    status: 'api-only',
  },
  {
    name: 'Authentication',
    phase: 'Phase 1',
    summary: 'Sign-in, sessions and first-run setup work over the API. No UI yet.',
    status: 'api-only',
  },
  {
    name: 'Peers',
    phase: 'Phase 3',
    summary: 'Which devices can reach each other, and how each link is carried.',
    status: 'planned',
  },
  {
    name: 'Groups',
    phase: 'Phase 6',
    summary: 'Collections of users and tagged devices that policy is written against.',
    status: 'planned',
  },
  {
    name: 'Access policies',
    phase: 'Phase 6',
    summary: 'Default-deny rules governing which devices may talk to which.',
    status: 'planned',
  },
  {
    name: 'Audit log',
    phase: 'Phase 6',
    summary: 'A record of every security-relevant change, and who made it.',
    status: 'planned',
  },
  {
    name: 'DNS',
    phase: 'Phase 7',
    summary: 'Names for devices, so nothing has to be reached by IP address.',
    status: 'planned',
  },
  {
    name: 'Routes',
    phase: 'Phase 8',
    summary: 'Subnets advertised into the network, and approval of them.',
    status: 'planned',
  },
  {
    name: 'Server settings',
    phase: 'Phase 12',
    summary: 'Address pools, relays and backup configuration.',
    status: 'planned',
  },
] as const;
