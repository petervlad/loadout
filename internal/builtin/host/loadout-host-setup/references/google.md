# Google sign-in (optional)

Adds "Continue with Google" next to email codes. It needs a Google Cloud project the person owns, a web OAuth client in it, and two settings. Skip it unless the team wants it. It does not work with simple login.

Verified how: not run end to end. Written from Google's OAuth documentation and loadout's own login code on 2026-10-07; Google's console menus move often, so follow the page the person sees and Google's current documentation.

Sources:
- https://developers.google.com/identity/protocols/oauth2/web-server (create credentials, "Web application", authorized redirect URIs)

## Steps

The person does these in their browser (a secret appears on screen once; you never see it):

1. In the Google Cloud console, pick or create a project. Under the OAuth consent screen (Google Auth platform) set the app name and a support email; choose **External** for people outside one Google Workspace, or **Internal** for one Workspace. The scopes loadout asks for are the basic ones: `openid`, `email` and `profile`. While the app is in "Testing", only the test users the person lists can sign in; publishing it removes that limit.
2. Create credentials, **OAuth client ID**, application type **Web application**.
3. Under **Authorized redirect URIs** add exactly `https://NAME/auth/google/callback`, with the registry's own public address.
4. Copy the client ID and the client secret into the settings file themselves, with their editor:

   ```sh
   LOADOUT_GOOGLE_CLIENT_ID=
   LOADOUT_GOOGLE_CLIENT_SECRET=
   ```

   Both or neither: one alone stops `loadout serve`.
5. Check with `loadout serve --check --env-file ~/loadout.env`: the `google` line says `on`. Deploy the file as the setup skill's step 3 says.

## Check

The person opens `https://NAME/auth/login`, chooses Continue with Google, and lands signed in. loadout accepts only an email Google marks verified; the person is found by Google account, else by that email, else created. An address a Google user signs in with is verified by Google, so no mailed code is sent.
