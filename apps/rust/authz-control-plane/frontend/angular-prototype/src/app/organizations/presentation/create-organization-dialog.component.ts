// Inline dialog using <dialog> + reactive form pattern via signals.
// Same label rules as the back-end MaterializedPath VO.
import {
  ChangeDetectionStrategy, Component, ElementRef, ViewChild,
  computed, inject, signal,
} from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { OrganizationsStore } from '../application/organizations.store';
import { isValidLabel } from '../domain/organization.model';

@Component({
  selector: 'app-create-organization-dialog',
  standalone: true,
  imports: [CommonModule, FormsModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <dialog #dlg class="dlg">
      <h3>Create organization</h3>
      <p class="muted">Root node (kind GROUP). Code becomes the first ltree label.</p>
      <label>
        <span>Code</span>
        <input [(ngModel)]="code" name="code" placeholder="acme" />
        <small class="err" *ngIf="codeError()">{{ codeError() }}</small>
      </label>
      <label>
        <span>Name</span>
        <input [(ngModel)]="name" name="name" placeholder="Acme Corporation" />
      </label>
      <small class="err" *ngIf="submitError()">{{ submitError() }}</small>
      <div class="actions">
        <button (click)="close()">Cancel</button>
        <button class="primary" [disabled]="!valid() || submitting()" (click)="submit()">
          {{ submitting() ? 'Creating…' : 'Create' }}
        </button>
      </div>
    </dialog>
  `,
  styles: [`
    .dlg {
      background: var(--surface); color: var(--fg);
      border: 1px solid var(--border); border-radius: 8px;
      padding: 20px; min-width: 360px;
    }
    .dlg label { display: block; margin: 8px 0; }
    .dlg label span { display: block; font-size: 13px; margin-bottom: 4px; }
    .dlg input { width: 100%; box-sizing: border-box; }
    .actions { display: flex; gap: 8px; justify-content: flex-end; margin-top: 16px; }
    .err { color: #f87171; display: block; margin-top: 4px; }
    .muted { color: var(--muted); margin-top: 0; }
  `],
})
export class CreateOrganizationDialogComponent {
  @ViewChild('dlg') private dlg!: ElementRef<HTMLDialogElement>;
  private readonly store = inject(OrganizationsStore);

  code = '';
  name = '';
  readonly submitting = signal(false);
  readonly submitError = signal<string | null>(null);

  readonly codeError = computed(() =>
    this.code && !isValidLabel(this.code)
      ? 'Code must match [a-z0-9_]{1,64}'
      : null,
  );

  readonly valid = computed(
    () => !this.codeError() && this.code.length > 0 && this.name.length > 0,
  );

  open(): void {
    this.code = '';
    this.name = '';
    this.submitError.set(null);
    this.dlg.nativeElement.showModal();
  }

  close(): void {
    this.dlg.nativeElement.close();
  }

  async submit(): Promise<void> {
    if (!this.valid()) return;
    this.submitting.set(true);
    this.submitError.set(null);
    try {
      await this.store.create({ code: this.code, name: this.name });
      this.close();
    } catch (e) {
      this.submitError.set((e as Error).message);
    } finally {
      this.submitting.set(false);
    }
  }
}
