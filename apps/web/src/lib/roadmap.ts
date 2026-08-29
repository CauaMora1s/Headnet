/**
 * The administrative areas the web UI will eventually cover.
 *
 * They are listed with the roadmap phase that will deliver each one, and are
 * rendered as visibly unavailable. Showing an empty "Devices" table would read
 * as "you have no devices" rather than "this has not been built", which is the
 * distinction this whole build is careful about.
 */
export interface PlannedArea {
  name: string;
  phase: string;
  summary: string;
}

export const PLANNED_AREAS: readonly PlannedArea[] = [
  {
    name: 'Devices',
    phase: 'Phase 1',
    summary: 'Enrol, inspect and revoke the machines on this network.',
  },
  {
    name: 'Users',
    phase: 'Phase 1',
    summary: 'Accounts, invitations and the identity provider behind them.',
  },
  {
    name: 'Authentication',
    phase: 'Phase 1',
    summary: 'Local accounts and OpenID Connect providers.',
  },
  {
    name: 'Peers',
    phase: 'Phase 3',
    summary: 'Which devices can reach each other, and how each link is carried.',
  },
  {
    name: 'Groups',
    phase: 'Phase 6',
    summary: 'Collections of users and tagged devices that policy is written against.',
  },
  {
    name: 'Access policies',
    phase: 'Phase 6',
    summary: 'Default-deny rules governing which devices may talk to which.',
  },
  {
    name: 'Audit log',
    phase: 'Phase 6',
    summary: 'A record of every security-relevant change, and who made it.',
  },
  {
    name: 'DNS',
    phase: 'Phase 7',
    summary: 'Names for devices, so nothing has to be reached by IP address.',
  },
  {
    name: 'Routes',
    phase: 'Phase 8',
    summary: 'Subnets advertised into the network, and approval of them.',
  },
  {
    name: 'Server settings',
    phase: 'Phase 12',
    summary: 'Address pools, relays and backup configuration.',
  },
] as const;
