import * as React from 'react';
import {inject, observer} from "mobx-react";
import NodeStore from "app/stores/NodeStore";
import Navbar from "react-bootstrap/Navbar";
import Nav from "react-bootstrap/Nav";
import {Dashboard} from "app/components/Dashboard";
import Badge from "react-bootstrap/Badge";
import {RouterStore} from 'mobx-react-router';
import {Explorer} from "app/components/Explorer";
import {NavExplorerSearchbar} from "app/components/NavExplorerSearchbar";
import {Redirect, Route, Switch} from 'react-router-dom';
import {LinkContainer} from 'react-router-bootstrap';
import {ExplorerMessageQueryResult} from "app/components/ExplorerMessageQueryResult";
import {ExplorerAddressQueryResult} from "app/components/ExplorerAddressResult";
import {Explorer404} from "app/components/Explorer404";
import {Faucet} from "app/components/Faucet";
import {Neighbors} from "app/components/Neighbors";
import {Visualizer} from "app/components/Visualizer";
import {Drng} from "app/components/Drng";
import {Mana} from "app/components/Mana";
import {ExplorerTransactionQueryResult} from "app/components/ExplorerTransactionQueryResult";
import {ExplorerOutputQueryResult} from "app/components/ExplorerOutputQueryResult";
import {ExplorerBranchQueryResult} from "app/components/ExplorerBranchQueryResult";

interface Props {
    history: any;
    routerStore?: RouterStore;
    nodeStore?: NodeStore;
}

@inject("nodeStore")
@inject("routerStore")
@observer
export class Root extends React.Component<Props, any> {
    renderDevTool() {
        if (process.env.NODE_ENV !== 'production') {
            const DevTools = require('mobx-react-devtools').default;
            return <DevTools/>;
        }
    }

    componentDidMount(): void {
        this.props.nodeStore.connect();
    }

    render() {
        return (
            <div className="container">
                <Navbar expand="lg" bg="light" variant="light" className={"mb-4"}>
                    <Navbar.Brand>GoShimmer</Navbar.Brand>
                    <Nav className="mr-auto">
                        <LinkContainer to="/dashboard">
                            <Nav.Link>Dashboard</Nav.Link>
                        </LinkContainer>
                        <LinkContainer to="/neighbors">
                            <Nav.Link>Neighbors</Nav.Link>
                        </LinkContainer>
                        <LinkContainer to="/explorer">
                            <Nav.Link>
                                Explorer
                            </Nav.Link>
                        </LinkContainer>
                        <LinkContainer to="/visualizer">
                            <Nav.Link>
                                Visualizer
                            </Nav.Link>
                        </LinkContainer>
                        <LinkContainer to="/drng">
                            <Nav.Link>
                                dRNG beacon
                            </Nav.Link>
                        </LinkContainer>
                        <LinkContainer to="/faucet">
                            <Nav.Link>
                                Faucet 
                            </Nav.Link>
                        </LinkContainer>
                        <LinkContainer to="/mana">
                            <Nav.Link>
                                Mana
                            </Nav.Link>
                        </LinkContainer>
                    </Nav>
                    <Navbar.Collapse className="justify-content-end">
                        <NavExplorerSearchbar/>
                        <Navbar.Text>
                            {!this.props.nodeStore.websocketConnected &&
                            <Badge variant="danger">WS not connected!</Badge>
                            }
                        </Navbar.Text>
                    </Navbar.Collapse>
                </Navbar>
                <Switch>
                    <Route exact path="/dashboard" component={Dashboard}/>
                    <Route exact path="/neighbors" component={Neighbors}/>
                    <Route exact path="/explorer/message/:id" component={ExplorerMessageQueryResult}/>
                    <Route exact path="/explorer/address/:id" component={ExplorerAddressQueryResult}/>
                    <Route exact path="/explorer/transaction/:id" component={ExplorerTransactionQueryResult}/>
                    <Route exact path="/explorer/output/:id" component={ExplorerOutputQueryResult}/>
                    <Route exact path="/explorer/branch/:id" component={ExplorerBranchQueryResult}/>
                    <Route exact path="/explorer/404/:search" component={Explorer404}/>
                    <Route exact path="/drng" component={Drng}/>
                    <Route exact path="/explorer" component={Explorer}/>
                    <Route exact path="/visualizer" component={Visualizer}/>
                    <Route exact path="/visualizer/history" component={Visualizer}/>
                    <Route exact path="/faucet" component={Faucet}/>
                    <Route exact path="/mana" component={Mana}/>
                    <Redirect to="/dashboard"/>
                </Switch>
                {this.props.children}
                {this.renderDevTool()}
            </div>
        );
    }
}
