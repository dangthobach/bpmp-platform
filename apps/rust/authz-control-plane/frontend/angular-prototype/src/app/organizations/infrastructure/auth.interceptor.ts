// Reads the bearer token from localStorage and stamps it onto outbound
// requests. Production wiring: replace with an OIDC adapter (Keycloak).
import { HttpInterceptorFn } from '@angular/common/http';

export const authInterceptor: HttpInterceptorFn = (req, next) => {
  const token = typeof localStorage !== 'undefined'
    ? localStorage.getItem('authz.token')
    : null;
  if (!token) return next(req);
  return next(
    req.clone({ setHeaders: { Authorization: `Bearer ${token}` } }),
  );
};
