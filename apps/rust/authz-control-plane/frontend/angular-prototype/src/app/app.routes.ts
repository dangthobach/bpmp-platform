import { Routes } from '@angular/router';

export const routes: Routes = [
  { path: '', redirectTo: 'organizations', pathMatch: 'full' },
  {
    path: 'organizations',
    loadComponent: () =>
      import('./organizations/presentation/organizations-page.component').then(
        (m) => m.OrganizationsPageComponent,
      ),
  },
];
