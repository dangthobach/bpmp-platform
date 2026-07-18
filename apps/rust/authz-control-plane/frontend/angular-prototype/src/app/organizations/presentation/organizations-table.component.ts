// Presentational, fully driven by Signals exposed by the store.
// No business logic; emits intent via the store.
import { ChangeDetectionStrategy, Component, computed, inject } from '@angular/core';
import { CommonModule } from '@angular/common';
import { OrganizationsStore } from '../application/organizations.store';
import { pathSegments } from '../domain/organization.model';

@Component({
  selector: 'app-organizations-table',
  standalone: true,
  imports: [CommonModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <table class="orgs-table">
      <thead>
        <tr>
          <th style="width:32px">
            <input
              type="checkbox"
              [checked]="allSelected()"
              (change)="store.toggleMany(visibleIds(), $any($event.target).checked)"
              aria-label="select all"
            />
          </th>
          <th>Code</th>
          <th>Name</th>
          <th>Path</th>
          <th style="text-align:right">Nodes</th>
          <th style="text-align:right">Version</th>
        </tr>
      </thead>
      <tbody>
        <tr *ngIf="store.loading() && store.rows().length === 0">
          <td colspan="6" class="muted">Loading…</td>
        </tr>
        <tr *ngIf="!store.loading() && store.rows().length === 0">
          <td colspan="6" class="muted">No organizations.</td>
        </tr>
        <tr *ngFor="let r of store.rows(); trackBy: trackId">
          <td>
            <input
              type="checkbox"
              [checked]="store.isSelected(r.id)"
              (change)="store.toggle(r.id)"
              [attr.aria-label]="'select ' + r.code"
            />
          </td>
          <td><strong>{{ r.code }}</strong></td>
          <td>{{ r.name }}</td>
          <td>
            <span class="badge" *ngFor="let seg of segments(r.rootPath)">
              {{ seg }}
            </span>
          </td>
          <td style="text-align:right">{{ r.nodeCount }}</td>
          <td style="text-align:right"><span class="badge gray">{{ r.version }}</span></td>
        </tr>
      </tbody>
    </table>
  `,
  styles: [`
    .orgs-table { width: 100%; border-collapse: collapse; }
    .orgs-table th, .orgs-table td {
      padding: 8px 10px; border-bottom: 1px solid var(--border); text-align: left;
    }
    .orgs-table th { font-weight: 600; color: var(--muted); }
    .badge {
      display: inline-block; padding: 2px 6px; margin-right: 4px;
      border-radius: 4px; background: var(--surface); border: 1px solid var(--border);
      font-size: 12px;
    }
    .badge.gray { color: var(--muted); }
    .muted { color: var(--muted); }
  `],
})
export class OrganizationsTableComponent {
  readonly store = inject(OrganizationsStore);

  readonly visibleIds = computed(() => this.store.rows().map((r) => r.id));
  readonly allSelected = computed(() => {
    const ids = this.visibleIds();
    return ids.length > 0 && ids.every((id) => this.store.isSelected(id));
  });

  segments = pathSegments;
  trackId = (_: number, r: { id: string }) => r.id;
}
