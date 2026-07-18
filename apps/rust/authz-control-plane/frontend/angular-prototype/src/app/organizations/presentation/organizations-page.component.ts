// Composes the feature: table + bulk bar + create dialog.
// Page only wires; it never knows about HTTP or domain rules.
import {
  ChangeDetectionStrategy, Component, OnInit, ViewChild, inject,
} from '@angular/core';
import { CommonModule } from '@angular/common';
import { OrganizationsStore } from '../application/organizations.store';
import { OrganizationsTableComponent } from './organizations-table.component';
import { BulkActionsBarComponent } from './bulk-actions-bar.component';
import { CreateOrganizationDialogComponent } from './create-organization-dialog.component';

@Component({
  selector: 'app-organizations-page',
  standalone: true,
  imports: [
    CommonModule,
    OrganizationsTableComponent,
    BulkActionsBarComponent,
    CreateOrganizationDialogComponent,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <section class="page">
      <header>
        <h2>Organizations</h2>
        <button class="primary" (click)="dialog.open()">New organization</button>
      </header>
      <app-bulk-actions-bar></app-bulk-actions-bar>
      <app-organizations-table></app-organizations-table>
      <p *ngIf="store.error()" class="err">{{ store.error() }}</p>
      <footer>
        <button [disabled]="store.offset() === 0" (click)="store.page(-1)">Prev</button>
        <button
          [disabled]="store.rows().length < store.limit()"
          (click)="store.page(1)"
        >Next</button>
      </footer>
      <app-create-organization-dialog #dialog></app-create-organization-dialog>
    </section>
  `,
  styles: [`
    .page { display: flex; flex-direction: column; gap: 16px; padding: 24px; }
    header { display: flex; align-items: center; justify-content: space-between; }
    header h2 { margin: 0; }
    footer { display: flex; gap: 8px; justify-content: flex-end; }
    .err { color: #f87171; }
  `],
})
export class OrganizationsPageComponent implements OnInit {
  readonly store = inject(OrganizationsStore);
  @ViewChild('dialog') dialog!: CreateOrganizationDialogComponent;

  async ngOnInit(): Promise<void> {
    await this.store.load();
  }
}
