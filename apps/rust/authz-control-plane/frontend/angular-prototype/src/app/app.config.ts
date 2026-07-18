// Composition root: this is the ONLY place that binds infrastructure
// implementations to the domain ports. Mirrors Hexagonal/DI patterns used
// in the back-end's `bootstrap` module.
import { ApplicationConfig, provideZoneChangeDetection } from '@angular/core';
import { provideRouter, withComponentInputBinding } from '@angular/router';
import { provideHttpClient, withInterceptors } from '@angular/common/http';

import { routes } from './app.routes';
import { ORGANIZATION_REPOSITORY } from './organizations/domain/organization.repository';
import { HttpOrganizationRepository } from './organizations/infrastructure/http-organization.repository';
import { authInterceptor } from './organizations/infrastructure/auth.interceptor';

export const appConfig: ApplicationConfig = {
  providers: [
    provideZoneChangeDetection({ eventCoalescing: true }),
    provideRouter(routes, withComponentInputBinding()),
    provideHttpClient(withInterceptors([authInterceptor])),
    {
      provide: ORGANIZATION_REPOSITORY,
      useExisting: HttpOrganizationRepository,
    },
  ],
};
