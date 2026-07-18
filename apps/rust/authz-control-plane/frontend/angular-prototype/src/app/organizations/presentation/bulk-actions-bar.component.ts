// Pure presentational component: subscribes to selection count via the store.
import { ChangeDetectionStrategy, Component, inject } from '@angular/core';
import { CommonModule } from '@angular/common';
import { OrganizationsStore } from '../application/organizations.store';

@Component({
  selector: 'app-bulk-actions-bar',
  standalone: true,
  imports: [CommonModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <div class="bar" *ngIf="store.selectedCount() > 0">
      <span><strong>{{ store.selectedCount() }}</strong> selected</span>
      <div class="spacer"></div>
      <button disabled>Export</button>
      <button class="danger" disabled>Deactivate</button>
      <button (click)="store.clearSelection()">Clear</button>
    </div>
  `,
  styles: [`
    .bar {
      display: flex; align-items: center; gap: 12px;
      padding: 8px 12px; border-radius: 6px;
      background: rgba(99,102,241,0.1); border: 1px solid var(--accent);
    }
    .spacer { flex: 1; }
    .danger { color: #f87171; }
  `],
})
export class BulkActionsBarComponent {
  readonly store = inject(OrganizationsStore);
}
