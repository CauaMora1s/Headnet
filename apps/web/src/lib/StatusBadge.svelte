<script lang="ts">
  import type { HealthStatus } from './api';

  interface Props {
    status: HealthStatus | 'unknown';
  }

  const { status }: Props = $props();

  // Colour alone must not carry the meaning: the label is always present, so
  // the state is legible to someone who cannot distinguish the hues.
  const styles: Record<string, string> = {
    ok: 'bg-emerald-100 text-emerald-900 dark:bg-emerald-950 dark:text-emerald-200',
    degraded: 'bg-amber-100 text-amber-900 dark:bg-amber-950 dark:text-amber-200',
    down: 'bg-rose-100 text-rose-900 dark:bg-rose-950 dark:text-rose-200',
    unknown: 'bg-slate-200 text-slate-700 dark:bg-slate-800 dark:text-slate-300',
  };

  const labels: Record<string, string> = {
    ok: 'Healthy',
    degraded: 'Degraded',
    down: 'Down',
    unknown: 'Unknown',
  };
</script>

<span
  class="inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium {styles[status] ??
    styles.unknown}"
>
  {labels[status] ?? labels.unknown}
</span>
