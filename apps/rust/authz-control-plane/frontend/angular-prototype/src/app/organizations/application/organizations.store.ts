// Application layer — feature store built on Angular Signals.
// No HTTP knowledge here; depends only on the repository port. Side-effects
// are wrapped in `firstValueFrom` so the store can sequence them.
import { Injectable, computed, inject, signal } from '@angular/core';
import { firstValueFrom } from 'rxjs';
import type { Organization } from '../domain/organization.model';
import {
  ORGANIZATION_REPOSITORY,
  type ListParams,
  type CreateOrganizationInput,
} from '../domain/organization.repository';

interface State {
  rows: readonly Organization[];
  loading: boolean;
  error: string | null;
  offset: number;
  limit: number;
  selected: ReadonlySet<string>;
}

const initial: State = {
  rows: [],
  loading: false,
  error: null,
  offset: 0,
  limit: 50,
  selected: new Set<string>(),
};

@Injectable({ providedIn: 'root' })
export class OrganizationsStore {
  private readonly repo = inject(ORGANIZATION_REPOSITORY);
  private readonly state = signal<State>(initial);

  readonly rows = computed(() => this.state().rows);
  readonly loading = computed(() => this.state().loading);
  readonly error = computed(() => this.state().error);
  readonly offset = computed(() => this.state().offset);
  readonly limit = computed(() => this.state().limit);
  readonly selectedCount = computed(() => this.state().selected.size);
  readonly isSelected = (id: string) => this.state().selected.has(id);

  async load(params?: Partial<ListParams>): Promise<void> {
    const merged: ListParams = {
      offset: params?.offset ?? this.state().offset,
      limit: params?.limit ?? this.state().limit,
    };
    this.state.update((s) => ({ ...s, ...merged, loading: true, error: null }));
    try {
      const rows = await firstValueFrom(this.repo.list(merged));
      this.state.update((s) => ({ ...s, rows, loading: false }));
    } catch (e) {
      this.state.update((s) => ({
        ...s,
        loading: false,
        error: (e as Error).message,
      }));
    }
  }

  async create(input: CreateOrganizationInput): Promise<void> {
    await firstValueFrom(this.repo.create(input));
    await this.load();
  }

  toggle(id: string): void {
    this.state.update((s) => {
      const next = new Set(s.selected);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return { ...s, selected: next };
    });
  }

  toggleMany(ids: readonly string[], on: boolean): void {
    this.state.update((s) => {
      const next = new Set(s.selected);
      for (const id of ids) {
        if (on) next.add(id);
        else next.delete(id);
      }
      return { ...s, selected: next };
    });
  }

  clearSelection(): void {
    this.state.update((s) => ({ ...s, selected: new Set<string>() }));
  }

  async page(delta: number): Promise<void> {
    const { offset, limit } = this.state();
    await this.load({ offset: Math.max(0, offset + delta * limit), limit });
  }
}
