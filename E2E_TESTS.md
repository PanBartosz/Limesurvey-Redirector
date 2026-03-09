# End-to-End Test Plan

## Environment

- Docker Compose stack built from `compose.yaml` and `e2e/compose.yaml`
- App URL: `http://127.0.0.1:18100`
- Mock LimeSurvey JSON-RPC + survey host: `http://127.0.0.1:19080`
- Admin account from `e2e/test.env`
- Two route users created during the run: `alice`, `bob`

## Scenarios Executed

1. Unauthenticated access to `/admin` redirects to `/admin/login`.
2. Admin login succeeds with the env-configured credentials.
3. Admin creates a JSON-RPC LimeSurvey instance that points to the mock RPC endpoint.
4. Admin creates two route users: `alice` and `bob`.
5. `alice` can log in and create a weighted route using the configured instance and the dynamic survey-row builder.
6. `alice` can edit the route name, slug, algorithm, fuzzy threshold, and target weights from the route detail page.
7. `alice` cannot access `/admin/instances` or `/admin/users`.
8. The simulation JSON for the route is accessible to the owner but does not contain sensitive instance fields such as `encrypted_password`, `username`, or `remotecontrol_url`.
9. `bob` cannot see `alice`'s route in the routes list.
10. `bob` gets `403` for `alice`'s route detail URL when the route ID is known.
11. Public redirect `/r/alpha-route-updated?token=abc123&src=mail` resolves to the mock survey with the weighted algorithm and preserves the query parameters.
12. The route detail page shows a recorded redirect decision after the public redirect executes and exposes the full route URL for copying.
13. Admin disables `bob`, which immediately invalidates `bob`'s existing session and blocks further login attempts.
14. Admin resets `alice`'s password, which immediately invalidates `alice`'s existing session and rejects the old password.
15. `alice` can log back in with the new password and delete her route.
16. Admin can delete `bob` and `alice` after they no longer own any routes.

## Execution Command

```bash
cd /home/bartosz/Nextcloud/projekty/limesurvey/limesurvey_redirector
./e2e/run.sh
```

## Expected Result

- The script exits with status `0`
- The Docker app remains healthy during the run
- The mock survey page shows survey `222` for the public redirect scenario
- Disabled users and password-reset users are forced back to `/admin/login`
