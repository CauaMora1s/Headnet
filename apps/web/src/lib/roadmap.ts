/**
 * The administrative areas the web UI will eventually cover.
 *
 * They are listed with the roadmap phase that will deliver each one, and are
 * rendered as visibly unavailable. Showing an empty table for something that
 * was never built would read as "you have none of these" rather than "this has
 * not been built", which is the distinction this whole build is careful about.
 *
 * Entries leave this list the moment their screens land. Devices and
 * authentication were both still listed here after their screens shipped,
 * which is the same lie told backwards.
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
    name: 'Setup keys',
    phase: 'Phase 1',
    summary:
      'Keys can be created, listed and revoked over the API. Without a screen ' +
      'for them, enrolling a device means reaching for curl.',
    status: 'api-only',
  },
  {
    name: 'Users',
    phase: 'Phase 1',
    summary: 'Accounts exist and can be created; the management screens do not.',
    status: 'api-only',
  },
  {
    name: 'Single sign-on and MFA',
    phase: 'Phase 11',
    summary: 'Local accounts work today. OIDC and second factors do not exist.',
    status: 'planned',
  },
  {
    name: 'Peers',
    phase: 'Phase 3',
    summary:
      'Which devices can reach each other, and how each link is carried. ' +
      'Devices existing does not mean they can reach each other: nothing does yet.',
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
