What is this project essentially?

Efficient checkpoint/restore is something that cloud platforms have but not really something afforded locally.




agentfs brings this to your hands.



What are the different stories for this project?
- A local time machine for your projects





what does this solve?
- Automatic version control . git is explicit time-dimension management. time-dimension can be implicit.
- Provides checkpoint-restore primitives for everything.



What is this not good at?
- portability. although... are we a single mount-step away? bundle the bundles.
- full forkability -- we want network namespaces!





what can be developed on top of this?
- full local app forkability
  - port
  - dns


"so my broader goal right now is to abstract over ports and
  create completely forkable apps. the annoying part is that
  ports are dynamically provisioned; no two apps can run on
  8080, for example. one idea is to have a router basically
  route http traffic based on the host header (via a (.localhost
  subdomain) and send to the right app instance. I want to
  minimize coordination though. ideally the app doesn't do
  anything special -- it just launches itself. but some minimal
  coordination is needed -- the app needs to also identify
  itself to the router, or identify itself to some something
  observable such that the router can go, find that, find the
  port of the app and the identity, and do its routing. help me
  brainstorm some designs to this. also, is there any precedent
  to this? esp in the cloud infra world. brainstorm and research
  with  me!"






Where do I want to take this?

